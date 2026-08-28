package replicate_test

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/replicate"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// authored is when the changes below were made, and the instant the passes run
// at.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// backoff is the base interval a failed delivery waits.
const backoff = 30 * time.Second

// fixture is the pass over the slice's doubles.
type fixture struct {
	usecase    *replicate.Replicate
	deliveries *apptest.DeliveryRepository
	log        *apptest.OperationRepository
	peers      *apptest.Peers
	clock      *apptest.Clock

	peer   uuid.UUID
	reader uuid.UUID
	phone  uuid.UUID
}

func newFixture() *fixture {
	deliveries := apptest.NewDeliveryRepository()
	log := apptest.NewOperationRepository()
	peers := apptest.NewPeers()
	clock := apptest.NewClock(authored)

	return &fixture{
		usecase:    replicate.New(deliveries, log, peers, clock, backoff, 100),
		deliveries: deliveries,
		log:        log,
		peers:      peers,
		clock:      clock,
		peer:       uuid.New(),
		reader:     uuid.New(),
		phone:      uuid.New(),
	}
}

// owe writes count changes for the reader and owes every one of them to the
// peer.
func (f *fixture) owe(t *testing.T, reader uuid.UUID, count int) []*operation.Operation {
	t.Helper()

	written := make([]*operation.Operation, 0, count)

	for index := range count {
		op, err := operation.New(uuid.New(), &operation.Props{
			UserID:      reader,
			DeviceID:    f.phone,
			Target:      operation.Target{Entity: operation.TargetEbook, ID: uuid.New()},
			Kind:        operation.KindUpdate,
			Delta:       operation.Delta{"title": json.RawMessage(`"Vidas Secas"`)},
			VectorClock: crdt.VectorClock{crdt.Author(f.phone): uint64(index) + 1},
			CreatedAt:   authored,
		})
		if err != nil {
			t.Fatalf("seeding the log: %v", err)
		}

		if _, err = f.log.Append(t.Context(), op); err != nil {
			t.Fatalf("seeding the log: %v", err)
		}

		f.deliveries.Owe(op.ID, f.peer)
		written = append(written, op)
	}

	return written
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()
	owed := f.owe(t, f.reader, 3)

	output, err := f.usecase.Execute(t.Context(), replicate.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Servers != 1:
		t.Errorf("the pass found %d peers owed anything, want one", output.Servers)
	case output.Offered != 3:
		t.Errorf("the pass offered %d changes, want the three that were owed", output.Offered)
	case output.Confirmed != 3:
		t.Errorf("the pass confirmed %d, want the three the peer answered about", output.Confirmed)
	}

	for _, row := range f.deliveries.Rows() {
		if row.IsPending() {
			t.Errorf("a delivery the peer confirmed is still owed: %s", row.OperationID)
		}
	}

	batches := f.peers.Offered()
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("the peer was offered %v", batches)
	}

	// The batch is the reader's history in the order this node committed it: a
	// batch carrying an update ahead of the insert it depends on would be
	// refused at the far end, and refused for ever.
	for index, id := range batches[0] {
		if id != owed[index].ID {
			t.Errorf("change %d was offered out of the order the log holds", index)
		}
	}
}

// A peer replicates many readers and the certificate identifies the node rather
// than any one of them, so the call names one reader and a batch that crossed
// readers could not be sent at all.
func TestExecuteOffersOneBatchPerReader(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.owe(t, f.reader, 2)
	f.owe(t, uuid.New(), 1)

	if _, err := f.usecase.Execute(t.Context(), replicate.Input{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	batches := f.peers.Offered()
	if len(batches) != 2 {
		t.Fatalf("the peer was offered %d batches, want one per reader", len(batches))
	}
}

// A peer belonging to another operator is unreachable often enough that
// retrying it at full rate would be this node's largest source of outbound
// traffic, so the try is counted and the row waits.
func TestExecuteCountsATryThatDidNotLand(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.owe(t, f.reader, 2)
	f.peers.Err = errs.New(errs.KindUnavailable, "no route to host")

	output, err := f.usecase.Execute(t.Context(), replicate.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Failed != 2 || output.Confirmed != 0 {
		t.Fatalf("the pass reported %+v, want both changes left owed", output)
	}

	for _, row := range f.deliveries.Rows() {
		switch {
		case !row.IsPending():
			t.Error("a delivery that never landed was closed")
		case row.Attempts != 1:
			t.Errorf("the delivery has been tried %d times, want the failure counted", row.Attempts)
		case row.LastError == "":
			t.Error("the failure was not recorded, so the operator has nothing to act on")
		}
	}

	// And it waits: a second pass at the same instant finds the row backed off.
	if _, err = f.usecase.Execute(t.Context(), replicate.Input{}); err != nil {
		t.Fatalf("the second pass: %v", err)
	}

	if offered := f.peers.Offered(); len(offered) != 1 {
		t.Errorf("the peer was offered %d batches, want the backoff to have held the second", len(offered))
	}
}

// A refusal is a verdict and not a delivery failure: the destination has read
// the change and will read it the same way again, so a queue that retried it
// would never drain.
func TestExecuteSettlesAChangeThePeerRefused(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.owe(t, f.reader, 1)
	refusal := operation.Rejected("this node holds no ebook with that identifier")
	f.peers.Refuse = &refusal

	output, err := f.usecase.Execute(t.Context(), replicate.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Confirmed != 1 {
		t.Errorf("the pass confirmed %d, want the refusal settled", output.Confirmed)
	}

	for _, row := range f.deliveries.Rows() {
		if row.IsPending() {
			t.Error("a change the peer refused is still owed, so the queue will never drain")
		}
	}
}

// What is retried is what the destination did not answer for, which is a call
// that was cut short rather than a change that was refused.
func TestExecuteLeavesOwedWhatThePeerDidNotAnswerFor(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.owe(t, f.reader, 3)
	f.peers.Silent = 2

	output, err := f.usecase.Execute(t.Context(), replicate.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Confirmed != 1 || output.Failed != 2 {
		t.Fatalf("the pass reported %+v, want one settled and two left owed", output)
	}

	pending := 0

	for _, row := range f.deliveries.Rows() {
		if row.IsPending() {
			pending++
		}
	}

	if pending != 2 {
		t.Errorf("%d deliveries are still owed, want the two the peer said nothing about", pending)
	}
}

// A node whose peers are up to date ticks for ever, and a pass that found
// nothing must cost nothing.
func TestExecuteOffersNothingWhenNothingIsOwed(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), replicate.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Servers != 0 || output.Offered != 0 {
		t.Errorf("the pass reported %+v on an empty queue", output)
	}

	if offered := f.peers.Offered(); len(offered) != 0 {
		t.Errorf("a peer was dialed with nothing to say: %v", offered)
	}
}

// The queue is durable and the backoff is in it, so a pass that could not read
// it has to fail rather than report a pass that did nothing.
func TestExecutePassesOnAFailureOfTheNode(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.deliveries.Err = errs.New(errs.KindUnavailable, "the queue is not answering")

	if _, err := f.usecase.Execute(t.Context(), replicate.Input{}); err == nil {
		t.Error("Execute reported a pass over a queue it could not read")
	}
}
