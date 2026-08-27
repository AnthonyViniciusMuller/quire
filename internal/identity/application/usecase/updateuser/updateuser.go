// Package updateuser changes a reader's own record (UC06, update).
//
// Only the shown name and the address are writable. Everything else about a
// reader is either their identity — the local name and the origin server, which
// together are the identifier RN09 makes unique — or derived from it, and
// changing the origin server is the migration of RF17 rather than an edit.
//
// C14 in docs/tcc-corrections.md is the finding this use case is written
// against and does not yet implement: the address is what UC08 recovers an
// account through, so changing it should prove the reader is present the way
// changing a password does. The contract has no field to carry that password,
// so the check belongs with the contract amendment rather than here.
package updateuser

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/updateuser: execute"

// CodeNothingToUpdate is a request that names no field. It is a malformed
// request rather than a write of nothing: a client that meant to change
// something and sent an empty mask should be told, not answered with the record
// it already had.
const CodeNothingToUpdate = "nothing_to_update"

// UpdateUser changes readers.
type UpdateUser struct {
	users       user.Repository
	localServer service.LocalServer
	clock       service.Clock
}

// UpdateUser satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateUser)(nil)

// New returns the use case over its dependencies.
func New(users user.Repository, localServer service.LocalServer, clock service.Clock) *UpdateUser {
	return &UpdateUser{users: users, localServer: localServer, clock: clock}
}

// Execute applies the fields the request carries.
func (u *UpdateUser) Execute(ctx context.Context, input Input) (Output, error) {
	if input.DisplayName == nil && input.Email == nil {
		return Output{}, errs.New(errs.KindInvalidArgument, "the request changes nothing").
			WithOp(opExecute).
			WithCode(CodeNothingToUpdate).
			WithField("update_mask", "it must name at least one of display_name and email")
	}

	reader, err := u.users.GetByID(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	now := u.clock.Now()

	// Both are applied to the entity before either is written, so a request
	// carrying one good field and one bad one changes nothing rather than half.
	if input.DisplayName != nil {
		displayName, parseErr := user.ParseDisplayName(*input.DisplayName)
		if parseErr != nil {
			return Output{}, parseErr
		}

		if renameErr := reader.Rename(displayName, now); renameErr != nil {
			return Output{}, renameErr
		}
	}

	if input.Email != nil {
		email, parseErr := user.ParseEmail(*input.Email)
		if parseErr != nil {
			return Output{}, parseErr
		}

		if changeErr := reader.ChangeEmail(email, now); changeErr != nil {
			return Output{}, changeErr
		}
	}

	err = u.users.Update(ctx, reader)
	if err != nil {
		return Output{}, err
	}

	federatedID, err := reader.FederatedID(u.localServer.Domain())
	if err != nil {
		return Output{}, err
	}

	return Output{User: reader, FederatedID: federatedID}, nil
}
