// Package resetpassword is the second half of UC08: it consumes the recovery
// credential and sets the new password.
//
// Every session of every device ends with the reset, and the reason is the
// situation itself: a reader is recovering precisely because they may not be
// the only party holding the old password, so leaving a session alive would
// leave whoever else had it signed in. The device records survive — a reader
// keeps their appliances and logs in again on each — because the identifiers
// those records carry are what every vector clock is keyed by.
package resetpassword

import (
	"context"
	"errors"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/resetpassword: execute"

// The stable machine-readable codes this use case raises.
const (
	// CodeNoCredential is a request that presents nothing.
	//
	//nolint:gosec // G101: this is the name of an error, not a credential.
	CodeNoCredential = "no_credential"
	// CodeInvalidCredential is a credential this node did not issue as a
	// recovery, or issued and has since spent or let expire. They are one
	// answer: which of them it was is a fact about somebody else's account.
	//
	//nolint:gosec // G101: this is the name of an error, not a credential.
	CodeInvalidCredential = "invalid_credential"
)

// ResetPassword sets new passwords.
type ResetPassword struct {
	users       user.Repository
	credentials credential.Repository
	hasher      service.HashService
	auth        service.AuthService
	clock       service.Clock
	transaction service.Transaction
}

// ResetPassword satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ResetPassword)(nil)

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	credentials credential.Repository,
	hasher service.HashService,
	auth service.AuthService,
	clock service.Clock,
	transaction service.Transaction,
) *ResetPassword {
	return &ResetPassword{
		users:       users,
		credentials: credentials,
		hasher:      hasher,
		auth:        auth,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute spends the credential and sets the password.
//
// Everything after the credential is checked happens in one unit of work, and
// the order inside it matters: the credential is spent first, in the single
// statement that refuses one already spent, so two callers holding the same
// message cannot both set a password. Whichever loses finds no row to update
// and takes its unit down with it.
func (r *ResetPassword) Execute(ctx context.Context, input Input) (Output, error) {
	if input.RecoveryToken == "" {
		return Output{}, errs.New(errs.KindInvalidArgument, "the request presents no credential").
			WithOp(opExecute).
			WithCode(CodeNoCredential).
			WithField("recovery_token", "it must carry the credential from the recovery message")
	}

	// Checked before anything is read or spent. A password the node would
	// refuse anyway must not cost the reader the credential they were sent —
	// there is only one of those, and it cannot be sent again.
	password := user.Password(input.NewPassword)

	err := password.Validate()
	if err != nil {
		return Output{}, err
	}

	err = r.transaction.Within(ctx, func(ctx context.Context) error {
		presented, presentErr := r.present(ctx, input.RecoveryToken)
		if presentErr != nil {
			return presentErr
		}

		if consumeErr := r.credentials.Consume(ctx, presented.ID); consumeErr != nil {
			return consumeErr
		}

		return r.reset(ctx, presented.UserID, password)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}

// present returns the credential the caller holds, having established that this
// node issued it as a recovery and that it is still usable.
func (r *ResetPassword) present(ctx context.Context, token string) (*credential.Credential, error) {
	issued, err := r.credentials.GetByTokenHash(ctx, r.auth.DigestOf(token))
	if errors.Is(err, errs.KindNotFound) {
		return nil, invalidCredential()
	}

	if err != nil {
		return nil, err
	}

	// A session credential is not a recovery. Accepting one here would let a
	// device that merely holds a session set the password, which is the check
	// ChangePassword makes with the current password instead.
	if issued.Kind != credential.KindPasswordRecovery {
		return nil, invalidCredential()
	}

	if !issued.Usable(r.clock.Now()) {
		return nil, invalidCredential()
	}

	return issued, nil
}

// reset writes the new digest and ends every session of every device.
func (r *ResetPassword) reset(ctx context.Context, userID uuid.UUID, password user.Password) error {
	reader, err := r.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	digest, err := r.hasher.Hash(string(password))
	if err != nil {
		return err
	}

	reader.ChangePassword(digest, r.clock.Now())

	err = r.users.Update(ctx, reader)
	if err != nil {
		return err
	}

	// Every session, on every device. The reader is recovering because they may
	// not be the only party holding the old password, and a session that
	// survived would leave whoever else had it signed in.
	err = r.credentials.ConsumeForUser(ctx, reader.ID, credential.KindSessionRefresh)
	if err != nil {
		return err
	}

	// And every other recovery still outstanding, so that a second message
	// already sent cannot be used to set the password again.
	return r.credentials.ConsumeForUser(ctx, reader.ID, credential.KindPasswordRecovery)
}

// invalidCredential is the one answer to every credential this node will not
// accept.
func invalidCredential() error {
	return errs.New(errs.KindUnauthenticated, "that credential is not valid").
		WithOp(opExecute).
		WithCode(CodeInvalidCredential)
}
