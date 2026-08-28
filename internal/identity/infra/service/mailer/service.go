// Package mailer is the development adapter of the delivery port: it writes the
// recovery credential to the log instead of sending it.
//
// It is what a node with no transport configured gets, so that UC08 can be
// exercised end to end on a laptop with no relay on it — and it refuses to be
// built outside the development profile. That refusal is the point. A node that
// wrote a reader's recovery credential to its logs in production would hand
// every recovery to whoever reads them, and logs are read by more people, and
// kept in more places, than a mailbox is.
//
// The transport beside it is internal/identity/infra/service/smtp, and the di
// picks between the two by which section of the configuration the deployment
// filled in. C13 in docs/tcc-corrections.md is why there was a period with only
// this one: the architecture the thesis describes names no component that can
// deliver to an address.
package mailer

import (
	"context"
	"log/slog"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opNew is the operation reported by this file, in the form the errs package
// expects.
const opNew = "identity/mailer: new"

// Service writes what it was asked to deliver.
type Service struct{}

// Service satisfies the port the use cases hold.
var _ service.Mailer = (*Service)(nil)

// New returns the development notifier, and refuses to return one anywhere
// else.
//
// The failure is a precondition rather than an argument error: nothing about
// the call is wrong, the deployment simply has no way to do what it is for.
func New(environment config.Environment) (*Service, error) {
	if environment.IsProduction() {
		return nil, errs.New(errs.KindFailedPrecondition,
			"this node has no way to deliver a password recovery, and will not write one to its logs").
			WithOp(opNew).
			WithField("QUIRE_MAIL_SMTP_HOST", "it must name the relay this node submits a recovery to")
	}

	return &Service{}, nil
}

// SendPasswordRecovery writes the credential where a developer can read it.
//
// The address is recorded and the credential is not folded into the message
// text, so that the two are separate fields of the record: a development log is
// still a log, and a value that reaches one should at least be greppable enough
// to be found and purged.
func (s *Service) SendPasswordRecovery(ctx context.Context, message service.RecoveryMessage) error {
	logging.From(ctx).InfoContext(ctx, "password recovery credential, not delivered: this node has no transport",
		slog.String("email", message.Email.String()),
		slog.String("recovery_token", message.Token),
		slog.Time("expires_at", message.ExpiresAt))

	return nil
}
