// Package updateuser changes a reader's own record (UC06, update).
//
// Only the shown name is writable. Everything else about a reader is either
// their identity — the local name and the origin server, which together are the
// identifier RN09 makes unique — or derived from it, and changing the origin
// server is the migration of RF17 rather than an edit.
//
// The address used to be the second writable field and is now changeemail's,
// which is C14 in docs/tcc-corrections.md: it is what UC08 recovers an account
// through, so changing it proves the reader is present the way changing a
// password does — and a field mask has no way to say that one of its paths
// needs a credential.
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
	if input.DisplayName == nil {
		return Output{}, errs.New(errs.KindInvalidArgument, "the request changes nothing").
			WithOp(opExecute).
			WithCode(CodeNothingToUpdate).
			WithField("update_mask", "it must name display_name")
	}

	reader, err := u.users.GetByID(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	now := u.clock.Now()

	// Applied to the entity before it is written, so that a request carrying a
	// field the domain refuses changes nothing rather than half — which is what
	// this shape is for and stays worth keeping with one field, since the next
	// writable one arrives beside it rather than instead of it.
	displayName, err := user.ParseDisplayName(*input.DisplayName)
	if err != nil {
		return Output{}, err
	}

	err = reader.Rename(displayName, now)
	if err != nil {
		return Output{}, err
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
