// Package requestrecovery is the first half of UC08: it sends a recovery
// credential to the address on record.
//
// The call answers the same way whether or not the address is registered here.
// That is the whole shape of it: an unauthenticated caller may ask about any
// address, so a reply that distinguished the two would turn the endpoint into a
// way to find out who has an account on this node — and an address is a stronger
// thing to learn about somebody than a local name, since it identifies them
// off this node as well.
//
// What that uniformity does not cover is how long the call takes, and C13 in
// docs/tcc-corrections.md is where that is written down: delivering to an
// address that exists costs a delivery, and closing the difference needs the
// delivery to be queued rather than awaited. This node has no queue, so the
// channel is named rather than pretended shut.
package requestrecovery

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/requestrecovery: execute"

// CodeNoAddress is a request that carries no address at all. It is a malformed
// request rather than an answer about any account, so unlike everything else
// here it is reported.
const CodeNoAddress = "no_address"

// RequestRecovery sends recovery credentials.
type RequestRecovery struct {
	users       user.Repository
	credentials credential.Repository
	auth        service.AuthService
	mailer      service.Mailer
	localServer service.LocalServer
	clock       service.Clock
	transaction service.Transaction
}

// RequestRecovery satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RequestRecovery)(nil)

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	credentials credential.Repository,
	auth service.AuthService,
	mailer service.Mailer,
	localServer service.LocalServer,
	clock service.Clock,
	transaction service.Transaction,
) *RequestRecovery {
	return &RequestRecovery{
		users:       users,
		credentials: credentials,
		auth:        auth,
		mailer:      mailer,
		localServer: localServer,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute issues a recovery credential and sends it, when there is somebody to
// send it to.
func (r *RequestRecovery) Execute(ctx context.Context, input Input) (Output, error) {
	if input.Email == "" {
		return Output{}, errs.New(errs.KindInvalidArgument, "the request carries no address").
			WithOp(opExecute).
			WithCode(CodeNoAddress).
			WithField("email", "it must carry the address the recovery is sent to")
	}

	reader, err := r.lookup(ctx, input.Email)
	if err != nil {
		return Output{}, err
	}

	// Nobody here by that address. The caller is told what a reader would be
	// told, which is nothing.
	if reader == nil {
		return Output{}, nil
	}

	message, err := r.issue(ctx, reader)
	if err != nil {
		return Output{}, err
	}

	r.deliver(ctx, message)

	return Output{}, nil
}

// lookup finds the reader at the address, or reports nobody.
//
// A reader this node only replicates is nobody for this purpose too: they have
// no address here and no password here (C03), and RN08 gives their recovery to
// their own origin server.
func (r *RequestRecovery) lookup(ctx context.Context, address string) (*user.User, error) {
	email, parseErr := user.ParseEmail(address)
	if parseErr != nil {
		//nolint:nilerr,nilnil // a malformed address reaches nobody, which is not a failure of this call.
		return nil, nil
	}

	originServerID, err := r.localServer.ID(ctx)
	if err != nil {
		return nil, err
	}

	reader, err := r.users.GetByEmail(ctx, originServerID, email)
	if errors.Is(err, errs.KindNotFound) {
		//nolint:nilnil // nobody at that address, which is not a failure of this call.
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if !reader.Authenticates() {
		//nolint:nilnil // a replicated reader recovers with their own origin server (RN08).
		return nil, nil
	}

	return reader, nil
}

// issue mints the credential, ends any recovery already outstanding, and stores
// the new one.
//
// The two writes are one unit of work, and ending the outstanding ones is what
// keeps a reader who asked twice from leaving two live credentials behind: the
// most recent request is the one they are acting on, and the earlier message is
// one they have already stopped watching for.
func (r *RequestRecovery) issue(ctx context.Context, reader *user.User) (service.RecoveryMessage, error) {
	secret, err := r.auth.IssueRecovery(r.clock.Now())
	if err != nil {
		return service.RecoveryMessage{}, err
	}

	issued, err := credential.NewRecovery(reader.ID, secret.Digest, secret.ExpiresAt)
	if err != nil {
		return service.RecoveryMessage{}, err
	}

	err = r.transaction.Within(ctx, func(ctx context.Context) error {
		consumeErr := r.credentials.ConsumeForUser(ctx, reader.ID, credential.KindPasswordRecovery)
		if consumeErr != nil {
			return consumeErr
		}

		return r.credentials.Create(ctx, issued)
	})
	if err != nil {
		return service.RecoveryMessage{}, err
	}

	return service.RecoveryMessage{
		Email:       reader.Email,
		DisplayName: reader.DisplayName,
		Token:       secret.Value,
		ExpiresAt:   secret.ExpiresAt,
	}, nil
}

// deliver sends the message, and reports a failure to the log rather than to
// the caller.
//
// A delivery can only fail for an address that exists, so an error passed on
// would be exactly the distinction the empty reply exists to remove. What the
// reader loses is one recovery attempt, which they can repeat; what an operator
// gets is the record, which is where a broken transport should show up.
func (r *RequestRecovery) deliver(ctx context.Context, message service.RecoveryMessage) {
	err := r.mailer.SendPasswordRecovery(ctx, message)
	if err == nil {
		return
	}

	logging.From(ctx).ErrorContext(ctx, "a password recovery could not be delivered", logging.Err(err))
}
