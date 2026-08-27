package credential

import (
	"context"
	"time"
	"uuid"
)

// Repository is the port through which the use cases of the identity slice
// issue, present and revoke credentials. It belongs to the domain; what
// satisfies it lives in internal/identity/infra/repository/credential.
type Repository interface {
	// Create stores the credential. Only its digest is written, which is what
	// the entity holds.
	Create(ctx context.Context, credential *Credential) error

	// GetByTokenHash reads the credential a caller has presented, by the digest
	// of what they presented. A digest matching nothing is
	// errs.KindNotFound.
	GetByTokenHash(ctx context.Context, tokenHash string) (*Credential, error)

	// Consume spends the credential, and reports errs.KindConflict when it had
	// already been spent.
	//
	// The check and the write are one statement rather than a read followed by
	// an update. Two devices presenting the same refresh credential at the same
	// instant must not both be answered with a session, and between a separate
	// read and write there is a window in which they would be.
	Consume(ctx context.Context, id uuid.UUID) error

	// ConsumeForDevice revokes every unconsumed credential of a device, which
	// is what unbinding one means for the sessions it holds.
	ConsumeForDevice(ctx context.Context, deviceID uuid.UUID) error

	// ConsumeForUser revokes every unconsumed credential of a reader, of the
	// kind given. A password reset ends every session of every device: the
	// reader is recovering precisely because they may not be the only party
	// holding the old password.
	ConsumeForUser(ctx context.Context, userID uuid.UUID, kind Kind) error

	// DeleteExpired removes the credentials that expired before the instant
	// given, and reports how many. Nothing depends on it running — an expired
	// credential is refused whether or not the row is still there — so it is a
	// housekeeping call and not part of any use case.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
