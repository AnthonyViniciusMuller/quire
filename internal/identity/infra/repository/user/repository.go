// Package user is the PostgreSQL adapter of the reader repository: it satisfies
// the port declared in internal/identity/domain/user and is the only place that
// knows identity.users exists.
//
// The executor is resolved from the context on every call rather than held. A
// use case that registers a reader and binds their first device wraps both
// repositories in one transaction, and this is what lets the same repository
// run inside it or outside it without being told which.
package user

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate         = "identity/user: create"
	opUpdate         = "identity/user: update"
	opDelete         = "identity/user: delete"
	opGetByID        = "identity/user: get by id"
	opGetByLocalName = "identity/user: get by local name"
	opGetByEmail     = "identity/user: get by email"
)

// The names of the two indexes enforcing RN09, as they appear in the driver
// error. They are what tells one uniqueness failure from the other: without
// them a duplicate address and a duplicate identifier are the same SQLSTATE.
const (
	constraintIdentifier = "users_identifier_key"
	constraintEmail      = "users_origin_email_key"
)

// Repository reads and writes readers in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ user.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in: the
// transaction a use case opened, or the pool.
func (r *Repository) queries(ctx context.Context) *identitydb.Queries {
	return identitydb.New(r.manager.Executor(ctx))
}

// Create inserts the reader, naming which of the two uniqueness rules of RN09
// was broken when one was.
func (r *Repository) Create(ctx context.Context, record *user.User) error {
	err := r.queries(ctx).CreateUser(ctx, identitydb.CreateUserParams{
		ID:             record.ID,
		OriginServerID: record.OriginServerID,
		LocalName:      record.LocalName.String(),
		DisplayName:    record.DisplayName.String(),
		Email:          optionalEmail(record.Email),
		PasswordHash:   optionalString(record.PasswordHash),
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	})

	switch {
	case persist.IsUniqueViolation(err, constraintIdentifier):
		return errs.Wrap(err, errs.KindAlreadyExists, "that name is already taken on this server").
			WithOp(opCreate).
			WithCode(user.CodeLocalNameTaken).
			WithField("local_name", "it belongs to another reader here; the same name on another server is free")
	case persist.IsUniqueViolation(err, constraintEmail):
		return errs.Wrap(err, errs.KindAlreadyExists, "that address is already registered on this server").
			WithOp(opCreate).
			WithCode(user.CodeEmailRegistered).
			WithField("email", "it is already in use here")
	default:
		return persist.Classify(err, opCreate)
	}
}

// Update writes back the four columns UC06 makes writable.
func (r *Repository) Update(ctx context.Context, record *user.User) error {
	rows, err := r.queries(ctx).UpdateUser(ctx, identitydb.UpdateUserParams{
		ID:           record.ID,
		DisplayName:  record.DisplayName.String(),
		Email:        optionalEmail(record.Email),
		PasswordHash: optionalString(record.PasswordHash),
		UpdatedAt:    record.UpdatedAt,
	})

	if persist.IsUniqueViolation(err, constraintEmail) {
		return errs.Wrap(err, errs.KindAlreadyExists, "that address is already registered on this server").
			WithOp(opUpdate).
			WithCode(user.CodeEmailRegistered).
			WithField("email", "it is already in use here")
	}

	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	// An UPDATE that matched nothing is not an error to PostgreSQL, and it is
	// exactly what a reader deleted between the read and the write looks like.
	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// Delete removes the reader and everything that cascades from them.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries(ctx).DeleteUser(ctx, id)
	if err != nil {
		return persist.Classify(err, opDelete)
	}

	if rows == 0 {
		return notFound(nil, opDelete)
	}

	return nil
}

// GetByID reads a reader by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*user.User, error) {
	row, err := r.queries(ctx).GetUserByID(ctx, id)
	if err != nil {
		return nil, read(err, opGetByID)
	}

	return toDomain(&row), nil
}

// GetByLocalName reads a reader by the pair RN09 makes unique.
func (r *Repository) GetByLocalName(
	ctx context.Context,
	originServerID uuid.UUID,
	localName user.LocalName,
) (*user.User, error) {
	row, err := r.queries(ctx).GetUserByLocalName(ctx, identitydb.GetUserByLocalNameParams{
		OriginServerID: originServerID,
		LocalName:      localName.String(),
	})
	if err != nil {
		return nil, read(err, opGetByLocalName)
	}

	return toDomain(&row), nil
}

// GetByEmail reads a reader by address. The statement folds case on both sides,
// as the uniqueness index does.
func (r *Repository) GetByEmail(
	ctx context.Context,
	originServerID uuid.UUID,
	email user.Email,
) (*user.User, error) {
	row, err := r.queries(ctx).GetUserByEmail(ctx, identitydb.GetUserByEmailParams{
		OriginServerID: originServerID,
		Email:          email.String(),
	})
	if err != nil {
		return nil, read(err, opGetByEmail)
	}

	return toDomain(&row), nil
}

// read classifies the failure of a lookup, answering the absence of a row in
// terms of the reader rather than of the table.
func read(err error, op string) error {
	if persist.IsNoRows(err) {
		return notFound(err, op)
	}

	return persist.Classify(err, op)
}

// notFound is the answer to a reader who is not here.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no such reader on this server").
		WithOp(op).
		WithCode(user.CodeNotFound)
}

// toDomain rebuilds the entity from the row.
//
// It restores rather than constructs: the row was validated when it was
// written, and a repository that refused to read back what the schema accepted
// would make a record unreachable rather than correct.
func toDomain(row *identitydb.IdentityUser) *user.User {
	props := user.Props{
		OriginServerID: row.OriginServerID,
		LocalName:      user.LocalName(row.LocalName),
		DisplayName:    user.DisplayName(row.DisplayName),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}

	// Both are null on a node that only replicates this reader (C03), and the
	// zero value is what the domain reads as absent.
	if row.Email != nil {
		props.Email = user.Email(*row.Email)
	}

	if row.PasswordHash != nil {
		props.PasswordHash = *row.PasswordHash
	}

	return user.Restore(row.ID, &props)
}

// optionalEmail renders an absent address as the NULL the column holds for a
// replicated reader.
func optionalEmail(email user.Email) *string {
	if email.IsZero() {
		return nil
	}

	value := email.String()

	return &value
}

// optionalString renders an empty value as NULL, for the same reason.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
