// Package token is the JOSE implementation of the authentication port: it
// signs the access tokens of RNF11, verifies them, publishes the public half as
// a JWKS, and mints the opaque credentials that outlive a single call.
//
// go-jose rather than a JWT library, and the reason is that the JWKS is not a
// footnote here. RNF11 requires the public keys to be published under
// /.well-known, RNF12 delegates validation to a service mesh that fetches that
// document, and go-jose builds the signer and the published key from the same
// value — so the kid in a token header and the kid in the document cannot
// disagree. A library that only signed would leave the encoding of the key to
// be written by hand, where a stripped leading zero in a coordinate produces a
// document that verifies nowhere. It also has no transitive dependencies, and
// its parser takes the algorithms it will accept as an argument, which is the
// defence against algorithm confusion built into the call rather than
// remembered by the caller.
//
// The algorithm follows the key the operator supplied. ES256 is the one to
// prefer: every JOSE implementation verifies it, Envoy included, and RNF12
// makes Envoy one of the verifiers.
package token

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"time"
	"uuid"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew          = "identity/token: new"
	opIssueAccess  = "identity/token: issue access token"
	opVerifyAccess = "identity/token: verify access token"
	opIssueSecret  = "identity/token: issue credential"
)

// secretBytes is how much randomness an opaque credential carries. Two hundred
// and fifty-six bits is past guessing, and base64 renders it in forty-three
// characters, well inside the column that holds its digest.
const secretBytes = 32

// minRSABits is the shortest RSA key this node will sign with. Below it the
// signature is not worth making.
const minRSABits = 2048

// clockSkewLeeway is how far apart the clock of a device and the clock of this
// node may be before a token is refused for it.
//
// It is the value go-jose applies by default, made explicit because it is a
// security parameter and not a detail: a minute against an access token that
// lives for fifteen is a small widening, and without any leeway a device whose
// clock runs seconds ahead cannot use the token it was just issued.
const clockSkewLeeway = time.Minute

// deviceClaim is the private claim naming the appliance a token was issued to.
// It is spelled out rather than abbreviated, because a claim nobody registered
// is read by people as often as by programs.
const deviceClaim = "device_id"

// Service signs, verifies and mints, over one signing key.
type Service struct {
	algorithm jose.SignatureAlgorithm
	signer    jose.Signer
	publicKey crypto.PublicKey

	// jwks is rendered once. The key does not change while the process runs,
	// and rotating it is a redeployment.
	jwks []byte

	issuer   string
	audience string

	accessTTL   time.Duration
	refreshTTL  time.Duration
	recoveryTTL time.Duration
}

// Service satisfies the port the use cases hold.
var _ service.AuthService = (*Service)(nil)

// New builds the service over the key in auth, for tokens addressed to
// audience — which is this node's federation domain, so that a token issued
// here cannot be presented anywhere else.
func New(auth *config.Auth, audience string) (*Service, error) {
	private, err := parsePrivateKey(auth.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}

	algorithm, err := algorithmFor(private)
	if err != nil {
		return nil, err
	}

	key := jose.JSONWebKey{Key: private, KeyID: auth.KeyID, Algorithm: string(algorithm), Use: "sig"}

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: algorithm, Key: key},
		// The typ header is what tells a verifier holding several kinds of
		// JOSE object that this one is a JWT.
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal,
			"the node could not build a signer for its key").WithOp(opNew)
	}

	document, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key.Public()}})
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal,
			"the node could not render its public keys").WithOp(opNew)
	}

	return &Service{
		algorithm:   algorithm,
		signer:      signer,
		publicKey:   key.Public().Key,
		jwks:        document,
		issuer:      auth.Issuer,
		audience:    audience,
		accessTTL:   auth.AccessTokenTTL,
		refreshTTL:  auth.RefreshTokenTTL,
		recoveryTTL: auth.PasswordResetTTL,
	}, nil
}

// IssueAccess signs a token for the reader and device.
func (s *Service) IssueAccess(userID, deviceID uuid.UUID, now time.Time) (string, service.Claims, error) {
	claims := service.Claims{
		TokenID:   uuid.New(),
		UserID:    userID,
		DeviceID:  deviceID,
		Issuer:    s.issuer,
		Audience:  s.audience,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.accessTTL),
	}

	token, err := jwt.Signed(s.signer).
		Claims(jwt.Claims{
			ID:       claims.TokenID.String(),
			Subject:  claims.UserID.String(),
			Issuer:   claims.Issuer,
			Audience: jwt.Audience{claims.Audience},
			IssuedAt: jwt.NewNumericDate(claims.IssuedAt),
			// Not before the instant it was issued. Without it a clock behind
			// this node's would accept a token it should not have seen yet.
			NotBefore: jwt.NewNumericDate(claims.IssuedAt),
			Expiry:    jwt.NewNumericDate(claims.ExpiresAt),
		}).
		Claims(map[string]any{deviceClaim: claims.DeviceID.String()}).
		Serialize()
	if err != nil {
		return "", service.Claims{}, errs.Wrap(err, errs.KindInternal,
			"the access token could not be signed").WithOp(opIssueAccess)
	}

	return token, claims, nil
}

// VerifyAccess checks the token and returns what it asserts.
func (s *Service) VerifyAccess(token string, now time.Time) (service.Claims, error) {
	// The algorithms this node accepts are the one it signs with, and nothing
	// else. Passing them here is what refuses a token whose header asks for a
	// weaker one — the substitution the "none" algorithm made famous, and the
	// HMAC-over-a-public-key variant of it.
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{s.algorithm})
	if err != nil {
		return service.Claims{}, invalidToken(err, "that token could not be read")
	}

	var (
		registered jwt.Claims
		private    map[string]any
	)

	if err := parsed.Claims(s.publicKey, &registered, &private); err != nil {
		return service.Claims{}, invalidToken(err, "that token was not signed by this node")
	}

	if err := registered.ValidateWithLeeway(jwt.Expected{
		Issuer:      s.issuer,
		AnyAudience: jwt.Audience{s.audience},
		Time:        now,
	}, clockSkewLeeway); err != nil {
		if errors.Is(err, jwt.ErrExpired) {
			return service.Claims{}, errs.Wrap(err, errs.KindUnauthenticated, "that token has expired").
				WithOp(opVerifyAccess).
				WithCode(service.CodeTokenExpired)
		}

		return service.Claims{}, invalidToken(err, "that token is not valid here")
	}

	return toClaims(&registered, private)
}

// IssueRefresh mints the credential a device presents to stay signed in.
func (s *Service) IssueRefresh(now time.Time) (service.Secret, error) {
	return s.issueSecret(now, s.refreshTTL)
}

// IssueRecovery mints the credential UC08 sends to the address on record.
func (s *Service) IssueRecovery(now time.Time) (service.Secret, error) {
	return s.issueSecret(now, s.recoveryTTL)
}

// issueSecret is what both minting methods share.
func (s *Service) issueSecret(now time.Time, ttl time.Duration) (service.Secret, error) {
	value := make([]byte, secretBytes)
	if _, err := rand.Read(value); err != nil {
		return service.Secret{}, errs.Wrap(err, errs.KindInternal,
			"the node could not generate a credential").WithOp(opIssueSecret)
	}

	credential := base64.RawURLEncoding.EncodeToString(value)

	return service.Secret{
		Value:     credential,
		Digest:    s.DigestOf(credential),
		ExpiresAt: now.Add(ttl),
	}, nil
}

// DigestOf is the digest a credential is stored and looked up by.
//
// A plain SHA-256 and not a password hash, which is the right choice for
// exactly the reason bcrypt is the right one for a password. A password is
// chosen by a person and is guessable, so verifying it has to be slow; this
// value is two hundred and fifty-six bits from the system's random source, so
// there is no dictionary to work through and a slow digest would only buy a
// bcrypt on every refresh — which every device makes more often than it makes
// anything else.
func (s *Service) DigestOf(presented string) string {
	digest := sha256.Sum256([]byte(presented))

	return hex.EncodeToString(digest[:])
}

// JWKS is the document published under /.well-known/jwks.json.
func (s *Service) JWKS() []byte { return s.jwks }

// toClaims reads the node's vocabulary out of the JWT's.
func toClaims(registered *jwt.Claims, private map[string]any) (service.Claims, error) {
	userID, err := uuid.Parse(registered.Subject)
	if err != nil {
		return service.Claims{}, invalidToken(err, "that token names no reader")
	}

	tokenID, err := uuid.Parse(registered.ID)
	if err != nil {
		return service.Claims{}, invalidToken(err, "that token has no identifier")
	}

	raw, ok := private[deviceClaim].(string)
	if !ok {
		return service.Claims{}, invalidToken(nil, "that token names no device")
	}

	deviceID, err := uuid.Parse(raw)
	if err != nil {
		return service.Claims{}, invalidToken(err, "that token names no device")
	}

	claims := service.Claims{
		TokenID:  tokenID,
		UserID:   userID,
		DeviceID: deviceID,
		Issuer:   registered.Issuer,
	}

	if len(registered.Audience) > 0 {
		claims.Audience = registered.Audience[0]
	}

	if registered.IssuedAt != nil {
		claims.IssuedAt = registered.IssuedAt.Time()
	}

	if registered.Expiry != nil {
		claims.ExpiresAt = registered.Expiry.Time()
	}

	return claims, nil
}

// invalidToken is the answer to everything a refresh cannot fix. The message is
// deliberately the same for all of them: which part of a token was wrong is a
// fact worth nothing to its legitimate holder and something to whoever is
// guessing.
func invalidToken(cause error, message string) error {
	return errs.Wrap(cause, errs.KindUnauthenticated, message).
		WithOp(opVerifyAccess).
		WithCode(service.CodeTokenInvalid)
}

// parsePrivateKey reads the signing key out of the PEM the operator configured,
// accepting the three encodings a key generator produces.
func parsePrivateKey(encoded config.Secret) (crypto.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded.Reveal()))
	if block == nil {
		return nil, errs.New(errs.KindFailedPrecondition,
			"the configured signing key is not PEM").WithOp(opNew)
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errs.Wrapf(err, errs.KindFailedPrecondition,
			"the configured signing key is a %q block this node cannot read", block.Type).WithOp(opNew)
	}

	return key, nil
}

// algorithmFor is the signature algorithm the key can produce. The node does
// not choose one and then demand a matching key: the operator supplies a key
// and this reads the only algorithm that fits it.
func algorithmFor(key crypto.PrivateKey) (jose.SignatureAlgorithm, error) {
	switch typed := key.(type) {
	case *ecdsa.PrivateKey:
		return ecdsaAlgorithm(typed)
	case ed25519.PrivateKey:
		return jose.EdDSA, nil
	case *rsa.PrivateKey:
		if typed.N.BitLen() < minRSABits {
			return "", errs.Newf(errs.KindFailedPrecondition,
				"the configured signing key is %d bits, and this node signs with at least %d",
				typed.N.BitLen(), minRSABits).WithOp(opNew)
		}

		return jose.RS256, nil
	default:
		return "", errs.New(errs.KindFailedPrecondition,
			"the configured signing key is of a kind this node cannot sign with").WithOp(opNew)
	}
}

// ecdsaAlgorithm pairs the curve with the digest RFC 7518 pairs it with. The
// two are not independent: ES256 is P-256 with SHA-256 and nothing else.
func ecdsaAlgorithm(key *ecdsa.PrivateKey) (jose.SignatureAlgorithm, error) {
	switch key.Curve {
	case elliptic.P256():
		return jose.ES256, nil
	case elliptic.P384():
		return jose.ES384, nil
	case elliptic.P521():
		return jose.ES512, nil
	default:
		return "", errs.New(errs.KindFailedPrecondition,
			"the configured signing key is on a curve this node cannot sign with").WithOp(opNew)
	}
}
