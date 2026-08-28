package sync_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identityapptest "github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	pulloperationsusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pulloperations"
	pushoperationsusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	syncstream "github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/sync"
	changesservice "github.com/anthonyvsmuller/quire/internal/sync/infra/service/changes"
)

// authored is when the changes below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is the stream controller over the slice's doubles and its real hub.
type fixture struct {
	controller *syncstream.Sync
	log        *apptest.OperationRepository
	hub        *changesservice.Hub
	push       *pushoperationsusecase.PushOperations

	reader uuid.UUID
	phone  uuid.UUID
	ctx    context.Context
}

func newFixture(t *testing.T, poll time.Duration) *fixture {
	t.Helper()

	log := apptest.NewOperationRepository()
	hub := changesservice.New()
	push := pushoperationsusecase.New(log, apptest.NewRecords(), apptest.NewClock(authored),
		apptest.NewTransaction(log), hub)
	pull := pulloperationsusecase.New(log)

	reader, phone := uuid.New(), uuid.New()

	return &fixture{
		controller: syncstream.New(push, pull, hub, poll),
		log:        log,
		hub:        hub,
		push:       push,
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

// run serves the stream in the background and reports what it returned.
func (f *fixture) run(t *testing.T, s *stream) <-chan error {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- f.controller.Handle(s) }()

	return done
}

// A device that has just reconnected drains what it missed through the stream
// it then leaves open.
func TestHandleDrainsTheBacklogTheDeviceMissed(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)
	f.append(t, 3)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(start(0))

	batch := s.batch(t)
	if len(batch) != 3 {
		t.Fatalf("the stream sent %d changes, want the whole backlog", len(batch))
	}

	for index, op := range batch {
		if op.GetPosition() != int64(index)+1 {
			t.Errorf("change %d came at position %d, want them in position order", index, op.GetPosition())
		}
	}

	s.close()

	if err := <-done; err != nil {
		t.Errorf("Handle: %v", err)
	}
}

// A change pushed by one device reaches the reader's other devices as it
// happens, which is what the hub is for and what makes this a stream rather
// than a poll.
func TestHandleSendsAChangeAnotherCallStoredWhileTheStreamWasOpen(t *testing.T) {
	t.Parallel()

	// The poll is an hour away, so anything that arrives arrived through the
	// hub.
	f := newFixture(t, time.Hour)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(start(0))

	// Nothing to drain, so the stream is now waiting. Another call grows the
	// log through the same use case a second device's push would.
	f.append(t, 1)
	f.hub.Announce(f.reader)

	if batch := s.batch(t); len(batch) != 1 {
		t.Fatalf("the stream sent %d changes, want the one that arrived", len(batch))
	}

	s.close()

	if err := <-done; err != nil {
		t.Errorf("Handle: %v", err)
	}
}

// A stream that missed every hint still finds the change, because the poll is
// what makes the hub a hint rather than the mechanism.
func TestHandleFindsAChangeNobodyToldItAbout(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 10*time.Millisecond)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(start(0))

	// Written without announcing it, which is what a second replica of this
	// node storing a change looks like from here.
	f.append(t, 1)

	if batch := s.batch(t); len(batch) != 1 {
		t.Fatalf("the stream sent %d changes, want the one the poll should have found", len(batch))
	}

	s.close()

	if err := <-done; err != nil {
		t.Errorf("Handle: %v", err)
	}
}

// The same push the unary call serves, on an open stream, answered with the
// verdicts.
func TestHandleStoresWhatTheDevicePushes(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(start(0))

	change := &quirev1.Operation{
		Id:           uuid.New().String(),
		DeviceId:     f.phone.String(),
		TargetEntity: quirev1.TargetEntity_TARGET_ENTITY_EBOOK,
		TargetId:     uuid.New().String(),
		Operation:    quirev1.OperationKind_OPERATION_KIND_UPDATE,
		Delta:        claimed(t, map[string]any{"title": "Vidas Secas"}),
		VectorClock:  &quirev1.VectorClock{Entries: map[string]uint64{f.phone.String(): 1}},
		CreatedAt:    &quirev1.HybridTimestamp{UnixMicros: authored.UnixMicro()},
	}

	s.offer(&quirev1.SyncRequest{Payload: &quirev1.SyncRequest_Push{
		Push: &quirev1.OperationBatch{Operations: []*quirev1.Operation{change}},
	}})

	results := s.results(t)
	if len(results) != 1 {
		t.Fatalf("the stream answered with %d verdicts, want one per change offered", len(results))
	}

	if results[0].GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
		t.Errorf("the verdict is %s (%s), want applied", results[0].GetOutcome(), results[0].GetDetail())
	}

	if f.log.Len() != 1 {
		t.Errorf("the log holds %d changes after the push", f.log.Len())
	}

	s.close()

	if err := <-done; err != nil {
		t.Errorf("Handle: %v", err)
	}
}

// The node keeps at most one page in flight: an operation written to a socket
// is not an operation written to disk, and a device that dies in between must
// come back to the same place.
func TestHandleSendsNoMoreThanOnePageBeforeItIsConfirmed(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)
	f.append(t, operation.DefaultPageSize+2)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(start(0))

	first := s.batch(t)
	if len(first) != operation.DefaultPageSize {
		t.Fatalf("the first page carries %d changes, want a full page", len(first))
	}

	// Nothing else arrives until the device says it has stored what it has.
	s.silent(t)

	s.offer(&quirev1.SyncRequest{Payload: &quirev1.SyncRequest_Ack{
		Ack: &quirev1.SyncAck{Position: first[len(first)-1].GetPosition()},
	}})

	if second := s.batch(t); len(second) != 2 {
		t.Errorf("the rest of the backlog is %d changes, want the two that were left", len(second))
	}

	s.close()

	if err := <-done; err != nil {
		t.Errorf("Handle: %v", err)
	}
}

// A stream that began with a push would be a device asking this node to store
// changes without saying where it had got to, and the node would have nothing
// to send it back.
func TestHandleRefusesAStreamThatDoesNotBeginWithItsCursor(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(&quirev1.SyncRequest{Payload: &quirev1.SyncRequest_Push{
		Push: &quirev1.OperationBatch{},
	}})

	err := <-done
	if status.Code(err) != codes.InvalidArgument && err == nil {
		t.Errorf("Handle = %v, want a refusal", err)
	}
}

// The listener has to be released, or a hub with a thousand streams that came
// and went wakes a thousand listeners nobody is reading.
func TestHandleStopsWatchingWhenTheStreamEnds(t *testing.T) {
	t.Parallel()

	f := newFixture(t, time.Hour)

	s := newStream(f.ctx)
	done := f.run(t, s)

	s.offer(start(0))

	// The stream is running once it has answered the cursor with silence.
	s.silent(t)

	if watching := f.hub.Watching(f.reader); watching != 1 {
		t.Fatalf("the hub has %d listeners, want the open stream", watching)
	}

	s.close()

	if err := <-done; err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if watching := f.hub.Watching(f.reader); watching != 0 {
		t.Errorf("the hub still has %d listeners after the stream ended", watching)
	}
}

// claimed renders the fields a change writes, as the contract carries them.
func claimed(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()

	delta, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("building a delta: %v", err)
	}

	return delta
}

// start is the first message every stream carries.
func start(after int64) *quirev1.SyncRequest {
	return &quirev1.SyncRequest{
		Payload: &quirev1.SyncRequest_Start{Start: &quirev1.SyncStart{AfterPosition: after}},
	}
}

// stream is a bidirectional stream a test drives from both ends.
type stream struct {
	grpc.ServerStream

	ctx      context.Context
	incoming chan *quirev1.SyncRequest
	outgoing chan *quirev1.SyncResponse
}

func newStream(ctx context.Context) *stream {
	return &stream{
		ctx:      ctx,
		incoming: make(chan *quirev1.SyncRequest, 8),
		outgoing: make(chan *quirev1.SyncResponse, 8),
	}
}

// Context is the context the call is served under.
func (s *stream) Context() context.Context { return s.ctx }

// Recv reads what the test offered.
func (s *stream) Recv() (*quirev1.SyncRequest, error) {
	request, open := <-s.incoming
	if !open {
		return nil, io.EOF
	}

	return request, nil
}

// Send records what the node wrote.
func (s *stream) Send(response *quirev1.SyncResponse) error {
	s.outgoing <- response

	return nil
}

// offer queues a message from the device.
func (s *stream) offer(request *quirev1.SyncRequest) { s.incoming <- request }

// close half-closes the device's end.
func (s *stream) close() { close(s.incoming) }

// batch waits for the next page of changes.
func (s *stream) batch(t *testing.T) []*quirev1.Operation {
	t.Helper()

	select {
	case response := <-s.outgoing:
		operations := response.GetOperations()
		if operations == nil {
			t.Fatalf("the stream sent %T, want a batch of changes", response.GetPayload())
		}

		return operations.GetOperations()
	case <-time.After(2 * time.Second):
		t.Fatal("the stream sent nothing")

		return nil
	}
}

// results waits for the verdicts on a push.
func (s *stream) results(t *testing.T) []*quirev1.OperationResult {
	t.Helper()

	for {
		select {
		case response := <-s.outgoing:
			if pushed := response.GetPushResult(); pushed != nil {
				return pushed.GetResults()
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the stream answered no verdicts")

			return nil
		}
	}
}

// silent asserts that nothing arrives for long enough to mean nothing is
// coming.
func (s *stream) silent(t *testing.T) {
	t.Helper()

	select {
	case response := <-s.outgoing:
		t.Fatalf("the stream sent %T when it should have been waiting", response.GetPayload())
	case <-time.After(100 * time.Millisecond):
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
		&grpc.UnaryServerInfo{FullMethod: quirev1.SyncService_Sync_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
