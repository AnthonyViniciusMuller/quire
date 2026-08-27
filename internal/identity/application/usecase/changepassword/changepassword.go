// Package changepassword replaces a reader's password (UC06, the credentials
// half).
//
// It asks for the current password, and the contract says why: a session proves
// that a device is unlocked, not that the reader is at it. That is the same
// check UC08 makes with a credential sent to the address on record, and it is
// what makes this call an act of the reader rather than of whoever is holding
// their phone.
//
// Every session of every device ends with it. The contract does not say so —
// only the reset does — but the answer has to be the same: a reader who changes
// their password is responding to a suspicion, and a session that survived
// would be the one they suspect. The cost is that they log in again on each
// appliance, which is what they were going to do anyway.
package changepassword

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/changepassword: execute"

// CodeWrongPassword is the current password not matching. It is deliberately
// not the same code the login raises: there the answer covers who the reader is
// as well, while here the reader is already established and only the proof
// failed.
const CodeWrongPassword = "wrong_password"

// ChangePassword replaces passwords.
type ChangePassword struct {
	users       user.Repository
	credentials credential.Repository
	hasher      service.HashService
	clock       service.Clock
	transaction service.Transaction
}

// ChangePassword satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ChangePassword)(nil)

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	credentials credential.Repository,
	hasher service.HashService,
	clock service.Clock,
	transaction service.Transaction,
) *ChangePassword {
	return &ChangePassword{
		users:       users,
		credentials: credentials,
		hasher:      hasher,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute replaces the password and ends every session.
func (c *ChangePassword) Execute(ctx context.Context, input Input) (Output, error) {
	// Checked before the current one is verified, so that a new password the
	// node would refuse costs no hashing and reveals nothing about the old one.
	password := user.Password(input.NewPassword)

	err := password.Validate()
	if err != nil {
		return Output{}, err
	}

	reader, err := c.users.GetByID(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	matched, err := c.hasher.Verify(input.CurrentPassword, reader.PasswordHash)
	if err != nil {
		return Output{}, err
	}

	if !matched {
		return Output{}, errs.New(errs.KindUnauthenticated, "that is not the current password").
			WithOp(opExecute).
			WithCode(CodeWrongPassword).
			WithField("current_password", "it does not match the password on record")
	}

	digest, err := c.hasher.Hash(input.NewPassword)
	if err != nil {
		return Output{}, err
	}

	reader.ChangePassword(digest, c.clock.Now())

	err = c.transaction.Within(ctx, func(ctx context.Context) error {
		if updateErr := c.users.Update(ctx, reader); updateErr != nil {
			return updateErr
		}

		return c.credentials.ConsumeForUser(ctx, reader.ID, credential.KindSessionRefresh)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}
