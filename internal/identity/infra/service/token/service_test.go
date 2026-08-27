package token_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/service/token"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The node this file's tokens are issued by.
const (
	testIssuer   = "https://quire-a.example"
	testAudience = "quire-a.example"
)

// now is a fixed instant, so that every expiry in this file is decided by
// arithmetic rather than by how long the test took.
func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// ecdsaKeyPEM is a fresh P-256 key, in the PKCS#8 encoding a key generator
// produces.
func ecdsaKeyPEM(t *testing.T) config.Secret {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating an ecdsa key: %v", err)
	}

	return encodePKCS8(t, key)
}

// ed25519KeyPEM is a fresh Ed25519 key.
func ed25519KeyPEM(t *testing.T) config.Secret {
	t.Helper()

	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating an ed25519 key: %v", err)
	}

	return encodePKCS8(t, key)
}

func encodePKCS8(t *testing.T, key any) config.Secret {
	t.Helper()

	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}

	return config.Secret(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

// newService builds a service over key, with the lifetimes the node defaults
// to.
func newService(t *testing.T, key config.Secret, keyID string) *token.Service {
	t.Helper()

	auth, err := token.New(&config.Auth{
		PrivateKeyPEM:    key,
		KeyID:            keyID,
		Issuer:           testIssuer,
		AccessTokenTTL:   15 * time.Minute,
		RefreshTokenTTL:  720 * time.Hour,
		PasswordResetTTL: time.Hour,
	}, testAudience)
	if err != nil {
		t.Fatalf("token.New: %v", err)
	}

	return auth
}

func TestIssueAndVerifyAccess(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "k1")
	userID, deviceID := uuid.New(), uuid.New()

	signed, issued, err := auth.IssueAccess(userID, deviceID, now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	if strings.Count(signed, ".") != 2 {
		t.Fatalf("the token %q is not a compact JWS", signed)
	}

	verified, err := auth.VerifyAccess(signed, now().Add(time.Minute))
	if err != nil {
		t.Fatalf("VerifyAccess: %v", err)
	}

	switch {
	case verified.UserID != userID:
		t.Error("the token does not name the reader it was issued for")
	case verified.DeviceID != deviceID:
		t.Error("the token does not name the device, which is what RN10 checks an operation against")
	case verified.TokenID != issued.TokenID:
		t.Error("the identifier the token carries is not the one that was issued")
	case verified.Issuer != testIssuer || verified.Audience != testAudience:
		t.Errorf("issuer and audience = %q, %q", verified.Issuer, verified.Audience)
	case !verified.ExpiresAt.Equal(now().Add(15 * time.Minute)):
		t.Errorf("ExpiresAt = %s, want the configured lifetime past the instant of issue", verified.ExpiresAt)
	}
}

// TestVerifyRejectsAnExpiredToken is the ordinary outcome of RNF11 asking for a
// short-lived token, and the client is meant to tell it apart so that it
// refreshes rather than asking the reader to log in again.
func TestVerifyRejectsAnExpiredToken(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "k1")

	signed, _, err := auth.IssueAccess(uuid.New(), uuid.New(), now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	if _, err := auth.VerifyAccess(signed, now().Add(time.Hour)); err == nil {
		t.Fatal("VerifyAccess an hour past expiry = nil, want an error")
	} else {
		if !errors.Is(err, errs.KindUnauthenticated) {
			t.Errorf("error = %v, want unauthenticated", err)
		}

		if code := errs.CodeOf(err); code != service.CodeTokenExpired {
			t.Errorf("code = %q, want %q", code, service.CodeTokenExpired)
		}
	}
}

// TestVerifyToleratesClockSkew is the other side of the same rule: a device
// whose clock runs a little ahead must still be able to use the token it was
// just issued.
func TestVerifyToleratesClockSkew(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "k1")

	signed, _, err := auth.IssueAccess(uuid.New(), uuid.New(), now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	if _, err := auth.VerifyAccess(signed, now().Add(-30*time.Second)); err != nil {
		t.Errorf("VerifyAccess half a minute before the token was issued: %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "k1")

	// Another node, with its own key, its own name and its own issuer.
	other, err := token.New(&config.Auth{
		PrivateKeyPEM:    ecdsaKeyPEM(t),
		KeyID:            "k1",
		Issuer:           "https://quire-b.example",
		AccessTokenTTL:   15 * time.Minute,
		RefreshTokenTTL:  720 * time.Hour,
		PasswordResetTTL: time.Hour,
	}, "quire-b.example")
	if err != nil {
		t.Fatalf("token.New: %v", err)
	}

	foreign, _, err := other.IssueAccess(uuid.New(), uuid.New(), now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	// Signed by a key this node does not have, and addressed to another node.
	// Either alone is enough to refuse it; RN08 gives authentication to the
	// origin server, and a token from a peer is not a session here.
	tests := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "not a jws", token: "not.a.token"},
		{name: "another node's token", token: foreign},
		{name: "a tampered payload", token: tamper(foreign)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := auth.VerifyAccess(test.token, now())
			if err == nil {
				t.Fatal("VerifyAccess = nil, want an error")
			}

			if !errors.Is(err, errs.KindUnauthenticated) {
				t.Errorf("error = %v, want unauthenticated", err)
			}

			if code := errs.CodeOf(err); code != service.CodeTokenInvalid {
				t.Errorf("code = %q, want %q: refreshing cannot fix any of these", code, service.CodeTokenInvalid)
			}
		})
	}
}

// TestVerifyRejectsAnotherAlgorithm covers the substitution the "none"
// algorithm made famous: the parser is told which algorithms this node signs
// with, so a token asking for a different one is refused before its signature
// is even considered.
func TestVerifyRejectsAnotherAlgorithm(t *testing.T) {
	t.Parallel()

	curves := newService(t, ecdsaKeyPEM(t), "k1")
	edwards := newService(t, ed25519KeyPEM(t), "k1")

	signed, _, err := edwards.IssueAccess(uuid.New(), uuid.New(), now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	if _, err := curves.VerifyAccess(signed, now()); err == nil {
		t.Error("a token signed with EdDSA verified against a node that signs with ES256")
	}
}

// TestJWKSPublishesOnlyThePublicHalf is what makes the document safe to serve to
// anybody, which is the whole of RNF11.
func TestJWKSPublishesOnlyThePublicHalf(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "signing-key-1")

	var document struct {
		Keys []map[string]any `json:"keys"`
	}

	if err := json.Unmarshal(auth.JWKS(), &document); err != nil {
		t.Fatalf("the published document is not JSON: %v", err)
	}

	if len(document.Keys) != 1 {
		t.Fatalf("the document publishes %d keys, want 1", len(document.Keys))
	}

	key := document.Keys[0]

	if _, private := key["d"]; private {
		t.Fatal("the document contains the private half of the signing key")
	}

	for field, want := range map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"alg": "ES256",
		"use": "sig",
		// The identifier a token's header carries, so that a verifier holding
		// several keys knows which one to check the signature with.
		"kid": "signing-key-1",
	} {
		if key[field] != want {
			t.Errorf("the published key has %s = %v, want %v", field, key[field], want)
		}
	}
}

func TestIssueRefreshAndRecovery(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "k1")

	refresh, err := auth.IssueRefresh(now())
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	recovery, err := auth.IssueRecovery(now())
	if err != nil {
		t.Fatalf("IssueRecovery: %v", err)
	}

	switch {
	case refresh.Value == "":
		t.Error("the credential handed to its holder is empty")
	case refresh.Digest == refresh.Value:
		t.Error("the digest is the credential, so a dump of the table could be replayed")
	case refresh.Digest != auth.DigestOf(refresh.Value):
		t.Error("the digest is not what a lookup of the presented credential would compute")
	case !refresh.ExpiresAt.Equal(now().Add(720 * time.Hour)):
		t.Errorf("the refresh credential expires at %s, want the configured lifetime", refresh.ExpiresAt)
	case !recovery.ExpiresAt.Equal(now().Add(time.Hour)):
		t.Errorf("the recovery credential expires at %s, want the shorter lifetime", recovery.ExpiresAt)
	}

	// A recovery credential outliving a refresh one would make UC08 the
	// weakest way into an account.
	if !recovery.ExpiresAt.Before(refresh.ExpiresAt) {
		t.Error("the recovery credential lives at least as long as the session one")
	}
}

// TestIssuedCredentialsAreDistinct is what stops one device's session from
// being another's.
func TestIssuedCredentialsAreDistinct(t *testing.T) {
	t.Parallel()

	auth := newService(t, ecdsaKeyPEM(t), "k1")

	first, err := auth.IssueRefresh(now())
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	second, err := auth.IssueRefresh(now())
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	if first.Value == second.Value || first.Digest == second.Digest {
		t.Error("two credentials minted in a row are the same value")
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()

	short, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating a short rsa key: %v", err)
	}

	tests := []struct {
		name string
		key  config.Secret
	}{
		{name: "not pem at all", key: "x"},
		{name: "a pem block that is not a key", key: config.Secret(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nonsense")}))},
		{name: "an rsa key below the floor", key: encodePKCS8(t, short)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := token.New(&config.Auth{
				PrivateKeyPEM:    test.key,
				KeyID:            "k1",
				Issuer:           testIssuer,
				AccessTokenTTL:   15 * time.Minute,
				RefreshTokenTTL:  720 * time.Hour,
				PasswordResetTTL: time.Hour,
			}, testAudience)
			if err == nil {
				t.Fatal("token.New = nil, want an error")
			}

			// A node that cannot sign has to fail while it is starting, not at
			// the first login, so the kind says the deployment is wrong.
			if !errors.Is(err, errs.KindFailedPrecondition) {
				t.Errorf("error = %v, want a failed precondition", err)
			}
		})
	}
}

// tamper flips a character of the payload, leaving a token whose signature no
// longer covers what it carries.
func tamper(signed string) string {
	parts := strings.Split(signed, ".")
	if len(parts) != 3 || parts[1] == "" {
		return signed
	}

	payload := []byte(parts[1])
	if payload[0] == 'A' {
		payload[0] = 'B'
	} else {
		payload[0] = 'A'
	}

	parts[1] = string(payload)

	return strings.Join(parts, ".")
}
