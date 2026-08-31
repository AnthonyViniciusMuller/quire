package watchoperations_test

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/watchoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// authored is when the changes below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is the use case over a log the test fills.
type fixture struct {
	usecase *watchoperations.WatchOperations
	log     *apptest.OperationRepository
	reader  uuid.UUID
	phone   uuid.UUID
}

func newFixture() *fixture {
	log := apptest.NewOperationRepository()

	return &fixture{
		usecase: watchoperations.New(log),
		log:     log,
		reader:  uuid.New(),
		phone:   uuid.New(),
	}
}

// append writes count changes into a reader's log.
func (f *fixture) append(t *testing.T, reader uuid.UUID, count int) {
	t.Helper()

	for index := range count {
		op, err := operation.New(uuid.New(), &operation.Props{
			UserID:      reader,
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

// TestExecuteReportsTheHead is the whole of what the use case does: the last
// position this node allocated, which is what a caller compares its own cursor
// against.
func TestExecuteReportsTheHead(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.append(t, f.reader, 3)

	output, err := f.usecase.Execute(t.Context(), watchoperations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.LastPosition != 3 {
		t.Errorf("the head is %d, want 3 — the position of the last change written",
			output.LastPosition)
	}
}

// TestExecuteReportsZeroForAnEmptyLog covers the reader who has just been bound
// and has never written anything.
//
// Zero is the answer, and it matters that it is: a caller watching from the
// beginning holds cursor zero, so a head of zero is what tells it there is
// nothing to pull rather than something at an unknown position.
func TestExecuteReportsZeroForAnEmptyLog(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), watchoperations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.LastPosition != 0 {
		t.Errorf("the head of an empty log is %d, want 0", output.LastPosition)
	}
}

// TestExecuteIsScopedToTheReader is the property the whole cursor rests on.
//
// A position is one node's order for one reader's log. A head that counted
// another reader's changes would send this reader pulling for operations that
// are not theirs and never arrive, and the cursor would never catch up.
func TestExecuteIsScopedToTheReader(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stranger := uuid.New()

	f.append(t, stranger, 5)
	f.append(t, f.reader, 2)

	output, err := f.usecase.Execute(t.Context(), watchoperations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.LastPosition != 2 {
		t.Errorf("the head is %d, want 2 — another reader's log was counted", output.LastPosition)
	}
}
