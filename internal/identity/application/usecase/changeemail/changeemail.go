// Package changeemail replaces the address a reader's account is recovered
// through (UC06, and C14 in docs/tcc-corrections.md).
//
// It is a use case of its own rather than a field of updateuser, and the reason
// is the one C14 is about. The address is not one registration field among
// others: it is the channel UC08 recovers an account through, so whoever can
// change it can have a recovery credential sent somewhere of their choosing and
// then set the password. A session proves that a device is unlocked, not that
// the reader is at it — which is exactly why changing a password asks for the
// current one, and this call asks for it for exactly the same reason.
//
// A field mask cannot express that. A request naming display_name and email
// would have to demand a password for both or accept one for neither, so the
// field left updateuser and became this.
//
// The password check stops a device left unlocked for a minute, which is the
// threat the check on ChangePassword was written for. It does not stop somebody
// who learned the password, and for them the notice sent to the previous
// address is the part that survives: it is how the reader finds out at all.
package changeemail

import (
	"context"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/changeemail: execute"

// CodeWrongPassword is the password not matching. It is the same code
// changepassword raises, and deliberately so: it is the same refusal, of the
// same proof, for the same reason.
const CodeWrongPassword = "wrong_password"

// ChangeEmail replaces addresses.
type ChangeEmail struct {
	users       user.Repository
	hasher      service.HashService
	mailer      service.Mailer
	localServer service.LocalServer
	clock       service.Clock
}

// ChangeEmail satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ChangeEmail)(nil)

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	hasher service.HashService,
	mailer service.Mailer,
	localServer service.LocalServer,
	clock service.Clock,
) *ChangeEmail {
	return &ChangeEmail{
		users:       users,
		hasher:      hasher,
		mailer:      mailer,
		localServer: localServer,
		clock:       clock,
	}
}

// Execute proves the reader is present and replaces the address.
func (c *ChangeEmail) Execute(ctx context.Context, input Input) (Output, error) {
	// Parsed before the password is verified, so that an address the node would
	// refuse costs no hashing — and, more to the point, so that a caller
	// guessing passwords learns nothing from how long a malformed request took.
	email, err := user.ParseEmail(input.Email)
	if err != nil {
		return Output{}, err
	}

	reader, err := c.users.GetByID(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	matched, err := c.hasher.Verify(input.Password, reader.PasswordHash)
	if err != nil {
		return Output{}, err
	}

	if !matched {
		return Output{}, errs.New(errs.KindUnauthenticated, "that is not the current password").
			WithOp(opExecute).
			WithCode(CodeWrongPassword).
			WithField("password", "it does not match the password on record")
	}

	// Kept before the entity is changed, because it is where the notice goes
	// and the entity is about to stop holding it.
	previous := reader.Email

	now := c.clock.Now()

	err = reader.ChangeEmail(email, now)
	if err != nil {
		return Output{}, err
	}

	err = c.users.Update(ctx, reader)
	if err != nil {
		return Output{}, err
	}

	c.notify(ctx, reader, previous, now)

	federatedID, err := reader.FederatedID(c.localServer.Domain())
	if err != nil {
		return Output{}, err
	}

	return Output{User: reader, FederatedID: federatedID}, nil
}

// notify tells the previous address, and reports a failure to the log rather
// than to the caller.
//
// The write has already happened. Answering the reader with an error would tell
// them a change failed that did not, and would leave them believing the address
// is still the old one — which is worse than a notice that did not arrive. What
// an operator gets is the record, which is where a broken transport shows up.
//
// It is called outside any transaction on purpose: a delivery is not something a
// database can roll back, and a notice sent for a change that was then undone is
// a reader told about something that did not happen.
func (c *ChangeEmail) notify(ctx context.Context, reader *user.User, previous user.Email, at time.Time) {
	err := c.mailer.SendEmailChanged(ctx, service.EmailChangedMessage{
		PreviousEmail: previous,
		NewEmail:      reader.Email,
		DisplayName:   reader.DisplayName,
		ChangedAt:     at,
	})
	if err == nil {
		return
	}

	logging.From(ctx).ErrorContext(ctx, "an address change notice could not be delivered", logging.Err(err))
}
