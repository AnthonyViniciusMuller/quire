// Package deleteuser removes a reader from this node, with everything that
// cascades from them (UC06, delete).
//
// It is not a migration. RF17 moves a reader to another origin server and keeps
// their library, their annotations and their reading positions; this ends them
// here. The two are different calls on purpose, and the password this one asks
// for is the difference in what they cost when the caller is wrong about which
// they wanted.
//
// What cascades is decided by the schema rather than here: identity.devices,
// identity.credentials and everything keyed by the reader are removed with them
// by their foreign keys. A use case that deleted them one by one would be a
// second, quieter definition of what belongs to a reader, and the two would
// drift.
package deleteuser

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/deleteuser: execute"

// CodeWrongPassword is the password not matching.
const CodeWrongPassword = "wrong_password"

// DeleteUser removes readers.
type DeleteUser struct {
	users  user.Repository
	hasher service.HashService
}

// DeleteUser satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*DeleteUser)(nil)

// New returns the use case over its dependencies.
func New(users user.Repository, hasher service.HashService) *DeleteUser {
	return &DeleteUser{users: users, hasher: hasher}
}

// Execute removes the reader.
func (d *DeleteUser) Execute(ctx context.Context, input Input) (Output, error) {
	reader, err := d.users.GetByID(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	matched, err := d.hasher.Verify(input.Password, reader.PasswordHash)
	if err != nil {
		return Output{}, err
	}

	if !matched {
		return Output{}, errs.New(errs.KindUnauthenticated, "that is not the reader's password").
			WithOp(opExecute).
			WithCode(CodeWrongPassword).
			WithField("password", "it does not match the password on record")
	}

	err = d.users.Delete(ctx, reader.ID)
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}
