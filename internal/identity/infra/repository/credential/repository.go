// Package credential is the PostgreSQL adapter of the credential repository: it
// satisfies the port declared in internal/identity/domain/credential and is the
// only place that knows identity.credentials exists.
package credential

import (
	"context"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate           = "identity/credential: create"
	opGetByTokenHash   = "identity/credential: get by token hash"
	opConsume          = "identity/credential: consume"
	opConsumeForDevice = "identity/credential: consume for device"
	opConsumeForUser   = "identity/credential: consume for user"
	opDeleteExpired    = "identity/credential: delete expired"
)

// Repository issues, presents and revokes credentials in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ credential.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *identitydb.Queries {
	return identitydb.New(r.manager.Executor(ctx))
}

// Create stores the credential. Only its digest is written.
func (r *Repository) Create(ctx context.Context, issued *credential.Credential) error {
	err := r.queries(ctx).CreateCredential(ctx, identitydb.CreateCredentialParams{
		ID:        issued.ID,
		UserID:    issued.UserID,
		DeviceID:  optionalID(issued.DeviceID),
		Kind:      issued.Kind.String(),
		TokenHash: issued.TokenHash,
		ExpiresAt: issued.ExpiresAt,
		Consumed:  issued.Consumed,
	})

	return persist.Classify(err, opCreate)
}

// GetByTokenHash reads the credential a caller has presented.
func (r *Repository) GetByTokenHash(ctx context.Context, tokenHash string) (*credential.Credential, error) {
	row, err := r.queries(ctx).GetCredentialByTokenHash(ctx, tokenHash)
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, errs.Wrap(err, errs.KindNotFound, "that credential is not valid").
				WithOp(opGetByTokenHash).
				WithCode(credential.CodeNotFound)
		}

		return nil, persist.Classify(err, opGetByTokenHash)
	}

	return toDomain(&row), nil
}

// Consume spends the credential, in one statement.
//
// Zero rows means the credential is no longer available — already used, revoked,
// or never there — and all three are refused the same way. Which of them it was
// is a fact about somebody else's account.
func (r *Repository) Consume(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries(ctx).ConsumeCredential(ctx, id)
	if err != nil {
		return persist.Classify(err, opConsume)
	}

	if rows == 0 {
		return errs.New(errs.KindConflict, "that credential has already been used").
			WithOp(opConsume).
			WithCode(credential.CodeSpent)
	}

	return nil
}

// ConsumeForDevice revokes every unconsumed credential of a device.
func (r *Repository) ConsumeForDevice(ctx context.Context, deviceID uuid.UUID) error {
	// Not optionalID: a caller revoking the credentials of the zero device
	// would otherwise pass NULL, which matches every recovery credential in the
	// table — the one comparison that must not be made loosely.
	_, err := r.queries(ctx).ConsumeCredentialsForDevice(ctx, &deviceID)

	return persist.Classify(err, opConsumeForDevice)
}

// ConsumeForUser revokes every unconsumed credential of a reader, of one kind.
func (r *Repository) ConsumeForUser(ctx context.Context, userID uuid.UUID, kind credential.Kind) error {
	_, err := r.queries(ctx).ConsumeCredentialsForUser(ctx, identitydb.ConsumeCredentialsForUserParams{
		UserID: userID,
		Kind:   kind.String(),
	})

	return persist.Classify(err, opConsumeForUser)
}

// DeleteExpired removes the credentials that expired before the instant given.
func (r *Repository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	rows, err := r.queries(ctx).DeleteExpiredCredentials(ctx, before)
	if err != nil {
		return 0, persist.Classify(err, opDeleteExpired)
	}

	return rows, nil
}

// toDomain rebuilds the entity from the row.
func toDomain(row *identitydb.IdentityCredential) *credential.Credential {
	props := credential.Props{
		UserID:    row.UserID,
		Kind:      credential.Kind(row.Kind),
		TokenHash: row.TokenHash,
		ExpiresAt: row.ExpiresAt,
		Consumed:  row.Consumed,
	}

	// Null on a password recovery, which names no device: the reader may be
	// recovering from an appliance that is not bound to the account at all.
	if row.DeviceID != nil {
		props.DeviceID = *row.DeviceID
	}

	return credential.Restore(row.ID, &props)
}

// optionalID renders the zero identifier as the NULL the column holds for a
// recovery credential.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == (uuid.UUID{}) {
		return nil
	}

	return &id
}
