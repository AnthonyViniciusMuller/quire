// Package credential is the temporary credential an origin server issues: the
// entity, the two kinds of it, and the port a repository has to satisfy.
//
// The credential itself is never held here and never reaches the database. What
// is stored is a digest of it, so that a dump of the table hands an attacker
// nothing that can be replayed: presenting the credential means presenting a
// value whose digest matches a row, and only the party it was issued to has
// that value.
//
// What the access token of RNF11 is doing absent from a package about
// credentials is C09 in docs/tcc-corrections.md: it is a JWT, verified by
// signature against the published keys, so no node stores one.
package credential

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by the constructors.
const opNew = "identity/credential: new"

// CodeInvalidCredential is the code carried by a credential that could not be
// issued: no owner, no digest, or no expiry.
//
//nolint:gosec // G101: this is the name of an error, not a credential.
const CodeInvalidCredential = "invalid_credential"

// maxTokenHashLength is the width identity.credentials.token_hash declares.
const maxTokenHashLength = 255

// Props is everything about a credential other than its identifier.
type Props struct {
	// UserID is the reader the credential belongs to.
	UserID uuid.UUID
	// DeviceID is the device a session refresh belongs to, and the zero value
	// on a password recovery.
	DeviceID uuid.UUID
	// Kind says which of the two this is.
	Kind Kind
	// TokenHash is the digest of the credential, never the credential.
	TokenHash string
	// ExpiresAt bounds its validity.
	ExpiresAt time.Time
	// Consumed covers both "already used" and "revoked". In either case the
	// credential must not be honoured again, and the two are one column because
	// no caller has ever needed to tell them apart.
	Consumed bool
}

// Credential is a temporary credential issued by this node (MER: token_acesso;
// identity.credentials).
type Credential struct {
	// ID is the primary key.
	ID uuid.UUID

	Props
}

// NewSession is the refresh credential issued to a device on login, and rotated
// on every refresh.
func NewSession(userID, deviceID uuid.UUID, tokenHash string, expiresAt time.Time) (*Credential, error) {
	if deviceID == (uuid.UUID{}) {
		return nil, invalid("device_id", "a session belongs to one device, and is revoked with it")
	}

	return newCredential(KindSessionRefresh, userID, deviceID, tokenHash, expiresAt)
}

// NewRecovery is the credential sent to the address on record in the first half
// of UC08.
func NewRecovery(userID uuid.UUID, tokenHash string, expiresAt time.Time) (*Credential, error) {
	return newCredential(KindPasswordRecovery, userID, uuid.UUID{}, tokenHash, expiresAt)
}

// Restore rebuilds a credential already stored.
func Restore(id uuid.UUID, props *Props) *Credential {
	return &Credential{ID: id, Props: *props}
}

// newCredential is what both constructors share.
func newCredential(
	kind Kind,
	userID, deviceID uuid.UUID,
	tokenHash string,
	expiresAt time.Time,
) (*Credential, error) {
	switch {
	case userID == (uuid.UUID{}):
		return nil, invalid("user_id", "a credential must name the reader it belongs to")
	case tokenHash == "":
		return nil, invalid("token_hash", "a credential is stored as a digest, and this one has none")
	case len(tokenHash) > maxTokenHashLength:
		return nil, invalid("token_hash", "the digest must be at most 255 characters long")
	case expiresAt.IsZero():
		return nil, invalid("expires_at", "a credential must expire")
	}

	return &Credential{
		ID: uuid.New(),
		Props: Props{
			UserID:    userID,
			DeviceID:  deviceID,
			Kind:      kind,
			TokenHash: tokenHash,
			ExpiresAt: expiresAt,
		},
	}, nil
}

// invalid is the error every rejected constructor argument raises.
func invalid(field, reason string) error {
	return errs.New(errs.KindInvalidArgument, "the credential could not be issued").
		WithOp(opNew).
		WithCode(CodeInvalidCredential).
		WithField(field, reason)
}

// Expired reports whether the credential's validity has run out at now.
func (c *Credential) Expired(now time.Time) bool { return !now.Before(c.ExpiresAt) }

// Usable reports whether the credential may be honoured at now: it has neither
// been consumed nor expired.
//
// A caller that finds it unusable answers the same way whichever of the two it
// was. Telling a holder that their credential merely expired confirms that it
// once existed, which is a fact about somebody else's account.
func (c *Credential) Usable(now time.Time) bool { return !c.Consumed && !c.Expired(now) }

// Consume marks the credential spent, which is what makes a refresh a rotation
// rather than a reuse: the credential presented is finished and the reply
// carries its replacement. One that reappears afterwards was copied.
func (c *Credential) Consume() { c.Consumed = true }

// BelongsToDevice reports whether the credential was issued to deviceID.
func (c *Credential) BelongsToDevice(deviceID uuid.UUID) bool {
	return c.DeviceID != (uuid.UUID{}) && c.DeviceID == deviceID
}
