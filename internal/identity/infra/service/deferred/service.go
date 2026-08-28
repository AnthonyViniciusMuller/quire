// Package deferred hands a message to a worker instead of delivering it on the
// call that asked for one.
//
// It is the second half of C13 in docs/tcc-corrections.md, and it closes a
// channel the first half cannot. RequestPasswordRecovery answers the same way
// whether or not the address is registered on this node, so that an
// unauthenticated caller cannot use it to find out who has an account here —
// but the reply is only half of what a caller observes. The other half is how
// long it took, and an address that exists costs a delivery while one that does
// not costs nothing. A delivery is by far the slowest thing on that path, so
// the two are trivially distinguishable by a caller with a stopwatch.
//
// Enqueueing takes the same time either way, which is what makes the reply
// uniform in the dimension the empty message could not reach. It is also what
// the transport needs in order to survive a relay that is down: the call has
// already been answered, so a failed delivery costs the reader one attempt
// rather than an error they cannot act on.
//
// It is a decorator rather than a behaviour of the transport, because the same
// argument holds for every adapter of the port: the node that writes the
// credential to its log has the same timing difference, on the same call, for
// the same reason. One package deferring one port is what keeps that from
// being written twice and fixed once.
//
// The queue is in memory and it is not durable. A node that is killed loses
// what it was holding, and the reader repeats the request — which is what they
// would do if the relay had been down for those seconds. Making it durable is a
// table and the worker pattern the sync slice already has; it is not free, and
// what it would buy is one recovery attempt across a restart.
package deferred

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opSend is the operation reported by this file, in the form the errs package
// expects.
const opSend = "identity/deferred: send password recovery"

// Service queues what the transport beside it delivers.
type Service struct {
	next    service.Mailer
	queue   chan pending
	timeout time.Duration
	logger  *slog.Logger
}

// pending is one delivery waiting for the worker.
//
// It carries the attributes of the call that asked for it, and not the call's
// context: that context is cancelled the moment the reply is written, which is
// before this delivery is attempted, so keeping it would be keeping something
// already dead. What is worth keeping is the request identifier, so that the
// record of a delivery can be found from the record of the call.
//
// The message is a closure rather than one of the two message types, because
// what the queue holds is a delivery and not a kind of mail: a struct with a
// field per message would grow a field per message, and the worker would grow a
// switch that has to agree with it. What is queued here is "the thing this call
// asked for, addressed to the transport it asked".
type pending struct {
	deliver func(context.Context, service.Mailer) error
	// what names the delivery in the log, since a closure cannot be read.
	what string
	from []slog.Attr
}

// Service satisfies the port the use cases hold, and wraps another adapter of
// it.
var _ service.Mailer = (*Service)(nil)

// New returns the queue in front of next.
func New(next service.Mailer, cfg *config.Mail, logger *slog.Logger) *Service {
	return &Service{
		next:    next,
		queue:   make(chan pending, cfg.QueueSize),
		timeout: cfg.DeliveryTimeout,
		logger:  logger,
	}
}

// SendPasswordRecovery accepts the message and returns.
//
// A full queue is reported rather than waited on. Waiting is the one thing this
// package exists to avoid: a call that blocked until there was room would take
// longer for an address that exists, which is the difference the queue was
// introduced to remove — and it would take longest exactly when the node is
// least able to deliver anything.
func (s *Service) SendPasswordRecovery(ctx context.Context, message service.RecoveryMessage) error {
	return s.enqueue(ctx, "a password recovery", func(ctx context.Context, next service.Mailer) error {
		return next.SendPasswordRecovery(ctx, message)
	})
}

// SendEmailChanged accepts the notice and returns, on the same terms.
//
// The timing argument that put the recovery in a queue does not apply to this
// one — the caller is authenticated and the address is their own, so there is
// nothing to learn from how long it took. What does apply is the other half: the
// address has already changed, and a relay that is down must not turn a write
// that succeeded into a call that failed.
func (s *Service) SendEmailChanged(ctx context.Context, message service.EmailChangedMessage) error {
	return s.enqueue(ctx, "an address change notice", func(ctx context.Context, next service.Mailer) error {
		return next.SendEmailChanged(ctx, message)
	})
}

// enqueue accepts one delivery, or reports that there is no room for it.
func (s *Service) enqueue(
	ctx context.Context, what string, deliver func(context.Context, service.Mailer) error,
) error {
	select {
	case s.queue <- pending{deliver: deliver, what: what, from: logging.Attrs(ctx)}:
		return nil
	default:
		return errs.Newf(errs.KindResourceExhausted,
			"there are more messages waiting to be delivered than this node will hold, and %s was refused",
			what).
			WithOp(opSend)
	}
}

// Run delivers what is queued and returns when ctx is cancelled.
//
// It returns nil on cancellation rather than the context's error, for the
// reason the replication worker does: it is one member of the errgroup that
// holds the node's two listeners, and a node shutting down is not a node that
// failed.
func (s *Service) Run(ctx context.Context) error {
	s.logger.InfoContext(ctx, "the recovery delivery worker is running",
		slog.Int("queue_size", cap(s.queue)))

	for {
		select {
		case <-ctx.Done():
			s.drain(ctx)
			s.logger.InfoContext(ctx, "the recovery delivery worker stopped")

			return nil
		case delivery := <-s.queue:
			s.deliver(ctx, &delivery)
		}
	}
}

// drain delivers what was already accepted when the node was asked to stop.
//
// Only what is in the queue, and on a context the shutdown has not cancelled: a
// reader who asked a moment before a rolling update should not have to ask
// again, and a worker that kept accepting new work here would be a node that
// does not stop. What it cannot rescue is a message the process never got to,
// which is the durability this package does not claim.
func (s *Service) drain(ctx context.Context) {
	waiting := len(s.queue)
	if waiting == 0 {
		return
	}

	s.logger.InfoContext(ctx, "delivering what was queued before the node stopped",
		slog.Int("waiting", waiting))

	// WithoutCancel and not the cancelled context it is derived from: the
	// deadline below is what bounds this, and the attributes travel with it so
	// that the record still names the call each delivery came from.
	shutdown := context.WithoutCancel(ctx)

	for range waiting {
		select {
		case delivery := <-s.queue:
			s.deliver(shutdown, &delivery)
		default:
			return
		}
	}
}

// deliver hands one message to the transport, with its own deadline and its own
// record.
//
// A failure is written down and nothing else. There is nobody left to report it
// to — the call that asked was answered before this ran — and the operator's
// log is where a broken transport should show up.
func (s *Service) deliver(ctx context.Context, delivery *pending) {
	ctx = logging.WithAttrs(ctx, delivery.from...)

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if err := delivery.deliver(ctx, s.next); err != nil {
		s.logger.ErrorContext(ctx, "a message could not be delivered",
			slog.String("message", delivery.what), logging.Err(err))

		return
	}

	s.logger.InfoContext(ctx, "a message was delivered", slog.String("message", delivery.what))
}
