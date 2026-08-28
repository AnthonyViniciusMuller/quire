package deferred_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/service/deferred"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// settleFor bounds how long a test waits for the worker to pick something up.
// It is generous because what is being checked is that the delivery happens at
// all, never how fast.
const settleFor = 5 * time.Second

// transport is the adapter the queue stands in front of.
type transport struct {
	// delivered receives every message that reached it.
	delivered chan service.RecoveryMessage
	// blocked, when not nil, holds each delivery until it is closed. It is how
	// a test makes the transport slow on purpose, which is the condition the
	// queue exists for.
	blocked chan struct{}
	// err is what every delivery reports.
	err error
}

func (t *transport) SendPasswordRecovery(_ context.Context, message service.RecoveryMessage) error {
	if t.blocked != nil {
		<-t.blocked
	}

	t.delivered <- message

	return t.err
}

// SendEmailChanged is here because the port has two methods, and the queue is
// checked through the one that has a token in it: what this package does is the
// same for both, and a second set of the same tests would drift from the first.
func (t *transport) SendEmailChanged(_ context.Context, _ service.EmailChangedMessage) error {
	return t.err
}

// theMessage is what every test here queues.
func theMessage() service.RecoveryMessage {
	return service.RecoveryMessage{
		Email:       user.Email("anthony@quire-a.example"),
		DisplayName: user.DisplayName("Anthony"),
		Token:       "recovery-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

// queueOf builds the queue over the transport, and runs the worker unless the
// test asked for it to stay still.
func queueOf(t *testing.T, next service.Mailer, size int, run bool) *deferred.Service {
	t.Helper()

	queue := deferred.New(next, &config.Mail{
		QueueSize:       size,
		DeliveryTimeout: settleFor,
	}, logging.Discard())

	if !run {
		return queue
	}

	ctx, stop := context.WithCancel(t.Context())

	var running sync.WaitGroup

	running.Add(1)

	go func() {
		defer running.Done()

		_ = queue.Run(ctx)
	}()

	t.Cleanup(func() {
		stop()
		running.Wait()
	})

	return queue
}

// The point of the package: the call that asks for a recovery is answered
// before the delivery is attempted, so it takes the same time for an address
// that exists as for one that does not.
func TestTheCallDoesNotWaitForTheDelivery(t *testing.T) {
	t.Parallel()

	slow := &transport{
		delivered: make(chan service.RecoveryMessage, 1),
		blocked:   make(chan struct{}),
	}

	queue := queueOf(t, slow, 4, true)

	accepted := make(chan error, 1)

	go func() { accepted <- queue.SendPasswordRecovery(t.Context(), theMessage()) }()

	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
		}
	case <-time.After(settleFor):
		t.Fatal("the call was still waiting while the transport was blocked")
	}

	// And the delivery does happen, once the transport is willing.
	close(slow.blocked)

	select {
	case message := <-slow.delivered:
		if message.Token != "recovery-token" {
			t.Errorf("the transport was handed %q", message.Token)
		}
	case <-time.After(settleFor):
		t.Fatal("the message never reached the transport")
	}
}

// A full queue is refused rather than waited on: a call that blocked until
// there was room would take longest exactly when the node is least able to
// deliver anything, which is the difference the queue removes.
func TestAFullQueueIsRefusedRatherThanWaitedOn(t *testing.T) {
	t.Parallel()

	// The worker does not run, so nothing leaves the queue.
	queue := queueOf(t, &transport{delivered: make(chan service.RecoveryMessage, 8)}, 1, false)

	if err := queue.SendPasswordRecovery(t.Context(), theMessage()); err != nil {
		t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
	}

	accepted := make(chan error, 1)

	go func() { accepted <- queue.SendPasswordRecovery(t.Context(), theMessage()) }()

	select {
	case err := <-accepted:
		if !errors.Is(err, errs.KindResourceExhausted) {
			t.Fatalf("SendPasswordRecovery() error = %v, want the queue to be exhausted", err)
		}
	case <-time.After(settleFor):
		t.Fatal("the call blocked on a full queue")
	}
}

// A reader who asked a moment before a rolling update should not have to ask
// again, so what was already accepted is delivered on the way out.
func TestWhatWasQueuedIsDeliveredWhenTheNodeStops(t *testing.T) {
	t.Parallel()

	next := &transport{delivered: make(chan service.RecoveryMessage, 4)}
	queue := deferred.New(next, &config.Mail{QueueSize: 4, DeliveryTimeout: settleFor}, logging.Discard())

	if err := queue.SendPasswordRecovery(t.Context(), theMessage()); err != nil {
		t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
	}

	// The worker is started with a context that is already cancelled, which is
	// the shutdown it would see if the node were stopped between the call and
	// the first pass.
	ctx, stop := context.WithCancel(t.Context())
	stop()

	if err := queue.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	select {
	case <-next.delivered:
	default:
		t.Error("the message queued before the shutdown was dropped")
	}
}

// A transport that failed is a reader who lost one attempt, not a node whose
// worker stopped: the errgroup member that returned would take the listeners
// down with it.
func TestADeliveryThatFailedDoesNotStopTheWorker(t *testing.T) {
	t.Parallel()

	failing := &transport{
		delivered: make(chan service.RecoveryMessage, 4),
		err:       errors.New("the relay is down"),
	}

	queue := queueOf(t, failing, 4, true)

	for range 2 {
		if err := queue.SendPasswordRecovery(t.Context(), theMessage()); err != nil {
			t.Fatalf("SendPasswordRecovery() error = %v, want nil", err)
		}
	}

	for attempt := range 2 {
		select {
		case <-failing.delivered:
		case <-time.After(settleFor):
			t.Fatalf("the worker stopped after %d deliveries", attempt)
		}
	}
}
