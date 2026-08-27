package service

import (
	"time"
	"uuid"
)

// The stable machine-readable codes an access token failure carries. A client
// branches on these to decide between refreshing and asking the reader to log
// in again.
const (
	// CodeTokenExpired is a well-formed token whose validity has run out. It is
	// the ordinary outcome of RNF11 asking for a short-lived one, and the
	// answer is to refresh.
	CodeTokenExpired = "token_expired"
	// CodeTokenInvalid is everything else: a bad signature, an algorithm this
	// node does not sign with, an issuer that is not this node, a claim that
	// cannot be read. Refreshing will not help.
	CodeTokenInvalid = "token_invalid"
)

// Claims is what an access token asserts, in the vocabulary of the node rather
// than of the JWT that carried it.
//
// A token is never stored (RNF11): it is verified by signature against the keys
// this node publishes, so these fields are all a request carries about who is
// making it, and the two identifiers are why the token names a device as well
// as a reader. RN10 accepts a synchronization operation only when the
// authenticated device is the one the operation declares, and the only thing
// that can say which device is authenticated is the token.
type Claims struct {
	// TokenID is the jti. Nothing depends on it, and it is what makes one
	// token's path through the logs followable.
	TokenID uuid.UUID
	// UserID is the sub: the reader the token speaks for.
	UserID uuid.UUID
	// DeviceID is the appliance the session belongs to.
	DeviceID uuid.UUID
	// Issuer is the iss, which is this node.
	Issuer string
	// Audience is the aud: the node the token may be presented to.
	Audience string
	// IssuedAt is the iat.
	IssuedAt time.Time
	// ExpiresAt is the exp, and is short by RNF11.
	ExpiresAt time.Time
}

// Secret is an opaque credential and everything the caller has to do with it.
//
// The two strings are deliberately separate. [Secret.Value] is handed to its
// holder and never stored; [Secret.Digest] is stored and never leaves the node.
// A single field would make it possible to write the wrong one, which is the
// mistake this type exists to prevent.
type Secret struct {
	// Value is the credential itself, which only its holder ever has.
	Value string
	// Digest is what identity.credentials keeps.
	Digest string
	// ExpiresAt is the bound the issuing service chose, so that a use case
	// does not have to know which of the configured lifetimes applies.
	ExpiresAt time.Time
}

// Session is what a device is issued when it logs in, and what it presents
// afterwards. It is the pair of the two mechanisms below, which is why more
// than one use case returns it: logging in produces one, and refreshing
// replaces one with another.
type Session struct {
	// AccessToken is the signed assertion of RNF11.
	AccessToken string
	// AccessTokenExpiresAt is when it stops being accepted, and it is soon.
	AccessTokenExpiresAt time.Time
	// RefreshToken is the opaque credential, handed over once and never stored.
	RefreshToken string
	// RefreshTokenExpiresAt bounds how long a device may stay away before it
	// has to authenticate again.
	RefreshTokenExpiresAt time.Time
}

// AuthService issues and checks the two things a session is made of: the
// short-lived access token of RNF11, and the opaque credentials that outlive a
// single call.
//
// The two are not the same mechanism and are deliberately not one method. An
// access token is a signed assertion that anybody holding the published keys
// can check without asking this node — which is what lets the service mesh
// validate it (RNF12) — and it cannot be revoked before it expires, which is
// why it is short. An opaque credential is a random value whose digest is a
// row, and revoking it is a single update.
type AuthService interface {
	// IssueAccess signs a token for the reader and device, and returns it with
	// the claims it asserts.
	IssueAccess(userID, deviceID uuid.UUID, now time.Time) (string, Claims, error)

	// VerifyAccess checks the signature, the issuer, the audience and the time
	// bounds, and returns what the token asserts.
	//
	// The instant is a parameter rather than read from the clock, so that the
	// interceptor and its tests agree on what "expired" means.
	VerifyAccess(token string, now time.Time) (Claims, error)

	// IssueRefresh mints the credential a device presents to stay signed in,
	// bounded by the refresh lifetime.
	IssueRefresh(now time.Time) (Secret, error)

	// IssueRecovery mints the credential UC08 sends to the address on record,
	// bounded by the shorter recovery lifetime.
	IssueRecovery(now time.Time) (Secret, error)

	// DigestOf is the digest of a credential a caller presented, which is what
	// a repository looks a row up by.
	DigestOf(presented string) string

	// JWKS is the document published under /.well-known/jwks.json (RNF11): the
	// public half of the signing key, so that a peer or the mesh verifies a
	// token without contacting this node.
	JWKS() []byte
}
