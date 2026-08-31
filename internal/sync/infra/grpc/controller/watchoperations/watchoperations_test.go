package watchoperations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identityapptest "github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	watchusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/watchoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/watchoperations"
	changesservice "github.com/anthonyvsmuller/quire/internal/sync/infra/service/changes"
)

// authored is when the changes below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// awaitFor bounds how long a test waits for a notification it expects. It is
// long because it is only ever reached on a failure — the hub wakes the stream
// in the same process, so the passing path does not wait.
const awaitFor = 2 * time.Second

// fixture is the controller over the slice's doubles and its real hub.
type fixture struct {
	controller *watchoperations.WatchOperations
	log        *apptest.OperationRepository
	hub        *changesservice.Hub

	reader uuid.UUID
	phone  uuid.UUID
	ctx    context.Context
}

// newFixture builds the controller with a poll interval the test controls. An
// hour means the ticker never fires, so what a test observes is the hub.
func newFixture(t *testing.T, poll time.Duration) *fixture {
	t.Helper()

	log := apptest.NewOperationRepository()
	hub := changesservice.New()
	reader, phone := uuid.New(), uuid.New()

	return &fixture{
		controller: watchoperations.New(watchusecase.New(log), hub, poll),
		log:        log,
		hub:        hub,
		reader:     reader,
		phone:      phone,
		ctx:        authenticated(t, reader, phone),
	}
}

// append writes count changes into the reader's log, behind the stream's back.
func (f *fixture) append(t *testing.T, count int) {
	t.Helper()

	for index := range count {
		op, err := operation.New(uuid.New(), &operation.Props{
			UserID:      f.reader,
			DeviceID:    f.phone,
			Target:      operation.Target{Entity: operation.TargetEbook, ID: uuid.New()},
			Kind:        operation.KindUpdate,
			Delta:       operation.Delta{"title": json.RawMessage(`"Vidas Secas"`)},
			VectorClock: crdt.VectorClock{crdt.Author(f.phone): uint64(index) + 1},
			CreatedAt:   authored.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatalf("seeding the log: %v", err)
		}

		if _, err = f.log.Append(t.Context(), op); err != nil {
			t.Fatalf("seeding the log: %v", err)
		}
	}
}

// run starts the stream and returns it with the function that ends it.
func (f *fixture) run(t *testing.T, after int64) (stream *watchStream, stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(f.ctx)
	watching := newWatchStream(ctx)
	finished := make(chan error, 1)

	go func() {
		finished <- f.controller.Handle(
			&quirev1.WatchOperationsRequest{AfterPosition: after}, watching)
	}()

	return watching, func() {
		cancel()

		select {
		case err := <-finished:
			if err != nil && status.Code(err) != codes.Canceled {
				t.Errorf("the stream ended with %v, want the cancellation", err)
			}
		case <-time.After(awaitFor):
			t.Error("the stream did not return after its context was cancelled")
		}
	}
}

// TestTheBacklogIsAnnouncedOnConnecting is why the request carries a cursor at
// all.
//
// A caller that connects after being away is behind, and a stream that reported
// only what happened next would leave it waiting for a change that may not come
// for hours — with a backlog sitting on the node the whole time.
func TestTheBacklogIsAnnouncedOnConnecting(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)
	f.append(t, 3)

	stream, stop := f.run(t, 0)
	defer stop()

	if position := stream.await(t); position != 3 {
		t.Errorf("the stream announced position %d on connecting, want the head at 3", position)
	}
}

// TestNothingIsAnnouncedToACallerThatIsUpToDate is the other half of the same
// property: the cursor is believed.
//
// A caller holding the head has nothing to pull, and a notification would send
// it to make a call that returns an empty page.
func TestNothingIsAnnouncedToACallerThatIsUpToDate(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)
	f.append(t, 3)

	stream, stop := f.run(t, 3)
	defer stop()

	stream.awaitSilence(t)
}

// TestAChangeWakesTheStream is UC10 restored for a caller that cannot hold the
// bidirectional stream open: something happened elsewhere, and this caller
// hears about it rather than waiting for its next poll.
func TestAChangeWakesTheStream(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)

	stream, stop := f.run(t, 0)
	defer stop()

	// An empty log at the cursor the caller sent, so nothing is announced yet.
	stream.awaitSilence(t)

	f.append(t, 2)
	f.hub.Announce(f.reader)

	if position := stream.await(t); position != 2 {
		t.Errorf("the stream announced position %d, want the head at 2", position)
	}
}

// TestAWakeWithNoNewPositionSaysNothing is what keeps the stream quiet.
//
// The hub wakes every listener of a reader on every push, and a caller that has
// already been told the log reaches position 2 learns nothing from hearing it
// again. Without the comparison a browser would be handed a notification per
// push per device, each one provoking a pull that returns an empty page.
func TestAWakeWithNoNewPositionSaysNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)
	f.append(t, 2)

	stream, stop := f.run(t, 0)
	defer stop()

	if position := stream.await(t); position != 2 {
		t.Fatalf("the stream announced position %d on connecting, want 2", position)
	}

	// A wake with the log unchanged, which is what a push of an operation this
	// node already held looks like.
	f.hub.Announce(f.reader)

	stream.awaitSilence(t)
}

// TestTheTickerReportsWhatTheHubMissed covers the limitation changes.Hub states
// about itself: it is in-process, so two streams open against two replicas of
// this node do not wake each other.
//
// The poll is what makes that a latency rather than a loss, and this asserts it
// by growing the log without announcing anything at all.
//
// It waits for the head rather than for one report, because a ticker this fast
// runs while the log is still being written and reporting a partial head is
// correct — the stream says how far the log has got, and it has got that far.
// What is asserted is that it arrives at the end without being told.
func TestTheTickerReportsWhatTheHubMissed(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 10*time.Millisecond)

	stream, stop := f.run(t, 0)
	defer stop()

	f.append(t, 4)

	stream.awaitReaching(t, 4)
}

// TestTheStreamStopsWatchingWhenItEnds is the leak the hub's own documentation
// warns about: a listener nobody released is one the hub wakes for as long as
// the node runs.
func TestTheStreamStopsWatchingWhenItEnds(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)

	_, stop := f.run(t, 0)

	// The stream registers before its first report, so waiting for the hub to
	// hold a listener is waiting for it to have started.
	deadline := time.Now().Add(awaitFor)
	for f.hub.Watching(f.reader) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if f.hub.Watching(f.reader) != 1 {
		t.Fatalf("the hub holds %d listeners while the stream is open, want 1",
			f.hub.Watching(f.reader))
	}

	stop()

	if watching := f.hub.Watching(f.reader); watching != 0 {
		t.Errorf("the hub still holds %d listeners after the stream ended", watching)
	}
}

// TestAStreamWithNoIdentityIsRefused is the one thing this controller settles
// before anything else: the reader comes from the session and never from the
// request, so a stream carrying no identity names nobody and is not served.
//
// It is refused as internal rather than unauthenticated, and that is
// authn.Require's decision rather than this controller's. A caller with no
// credential never reaches here — the interceptor answers it — so a controller
// that finds no identity has been reached through a chain that is missing one,
// which is this node's fault and not the caller's.
func TestAStreamWithNoIdentityIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)

	err := f.controller.Handle(&quirev1.WatchOperationsRequest{},
		newWatchStream(t.Context()))
	if err == nil {
		t.Fatal("a stream with no identity was served")
	}

	// The kind and not a gRPC code: a controller raises an errs.Error, and the
	// interceptor chain is what turns one into a status. Asserting the code
	// here would be asserting a translation this package does not perform.
	if kind := errs.KindOf(err); kind != errs.KindInternal {
		t.Errorf("the refusal is %v, want %v", kind, errs.KindInternal)
	}
}

// watchStream is the server half of the call, which a test reads from.
type watchStream struct {
	grpc.ServerStream

	ctx      context.Context
	outgoing chan *quirev1.WatchOperationsResponse
}

func newWatchStream(ctx context.Context) *watchStream {
	return &watchStream{ctx: ctx, outgoing: make(chan *quirev1.WatchOperationsResponse, 8)}
}

// Context is the context the call is served under.
func (s *watchStream) Context() context.Context { return s.ctx }

// Send records what the node wrote.
func (s *watchStream) Send(response *quirev1.WatchOperationsResponse) error {
	s.outgoing <- response

	return nil
}

// await reads the next announced position, or fails.
func (s *watchStream) await(t *testing.T) int64 {
	t.Helper()

	select {
	case response := <-s.outgoing:
		return response.GetLastPosition()
	case <-time.After(awaitFor):
		t.Fatal("the stream announced nothing")

		return 0
	}
}

// awaitReaching reads announcements until one carries the position wanted.
//
// Every report before it must still be an advance, which is what stops this
// from passing on a stream that announced the same number repeatedly and
// happened to be right once.
func (s *watchStream) awaitReaching(t *testing.T, want int64) {
	t.Helper()

	deadline := time.After(awaitFor)

	var previous int64

	for {
		select {
		case response := <-s.outgoing:
			position := response.GetLastPosition()
			if position <= previous {
				t.Fatalf("the stream announced %d after %d, which is not an advance",
					position, previous)
			}

			if position == want {
				return
			}

			previous = position
		case <-deadline:
			t.Fatalf("the stream reached %d, want %d", previous, want)
		}
	}
}

// awaitSilence fails if anything is announced.
//
// It is a short wait on purpose: it is spent on every passing run, and what it
// is looking for would arrive at once — the hub wakes in the same process, and
// a report happens before Handle returns to its select.
func (s *watchStream) awaitSilence(t *testing.T) {
	t.Helper()

	select {
	case response := <-s.outgoing:
		t.Errorf("the stream announced position %d, want silence", response.GetLastPosition())
	case <-time.After(50 * time.Millisecond):
	}
}

// authenticated is a context carrying an identity, built by running the real
// interceptor rather than by reaching into it.
func authenticated(t *testing.T, reader, device uuid.UUID) context.Context {
	t.Helper()

	auth := identityapptest.NewAuthService()
	clock := identityapptest.NewClock(authored)

	token, _, err := auth.IssueAccess(reader, device, clock.Now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	incoming := metadata.NewIncomingContext(t.Context(),
		metadata.Pairs("authorization", "Bearer "+token))

	var served context.Context

	_, err = authn.New(auth, clock, nil).Unary()(incoming, nil,
		&grpc.UnaryServerInfo{FullMethod: quirev1.SyncService_WatchOperations_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
