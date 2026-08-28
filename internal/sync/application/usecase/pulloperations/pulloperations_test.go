package pulloperations_test

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pulloperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// authored is when the changes below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is the use case over a log the test fills.
type fixture struct {
	usecase *pulloperations.PullOperations
	log     *apptest.OperationRepository
	reader  uuid.UUID
	phone   uuid.UUID
}

func newFixture() *fixture {
	log := apptest.NewOperationRepository()

	return &fixture{
		usecase: pulloperations.New(log),
		log:     log,
		reader:  uuid.New(),
		phone:   uuid.New(),
	}
}

// append writes count changes into the reader's log.
func (f *fixture) append(t *testing.T, count int) []*operation.Operation {
	t.Helper()

	written := make([]*operation.Operation, 0, count)

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

		written = append(written, op)
	}

	return written
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()
	written := f.append(t, 3)

	output, err := f.usecase.Execute(t.Context(), pulloperations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case len(output.Operations) != 3:
		t.Fatalf("the page carries %d changes, want the whole log", len(output.Operations))
	case output.HasMore:
		t.Error("the whole log came back and the reply says there is more")
	case output.LastPosition != written[2].Position:
		t.Errorf("the cursor to continue from is %d, want %d", output.LastPosition, written[2].Position)
	}

	for index, op := range output.Operations {
		if op.Position != int64(index)+1 {
			t.Errorf("change %d is at position %d, want the page in position order", index, op.Position)
		}
	}
}

// A caller that has seen position N has necessarily seen every position below
// it, which is what allocating the number inside the writing transaction buys
// and what lets the cursor be a single number rather than a set.
func TestExecuteWalksTheWholeLogExactlyOnce(t *testing.T) {
	t.Parallel()

	f := newFixture()
	written := f.append(t, 7)

	seen := make(map[uuid.UUID]int, len(written))
	cursor := int64(0)

	for range len(written) {
		output, err := f.usecase.Execute(t.Context(), pulloperations.Input{
			UserID:        f.reader,
			AfterPosition: cursor,
			Limit:         2,
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}

		for _, op := range output.Operations {
			seen[op.ID]++
		}

		cursor = output.LastPosition

		if !output.HasMore {
			break
		}
	}

	if len(seen) != len(written) {
		t.Fatalf("the walk saw %d changes, want all %d", len(seen), len(written))
	}

	for id, count := range seen {
		if count != 1 {
			t.Errorf("%s came back %d times", id, count)
		}
	}
}

// A device that has drained the log and asks again must not be sent back to
// the beginning of it.
func TestExecuteLeavesTheCursorWhereItWasOnAnEmptyPage(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.append(t, 2)

	output, err := f.usecase.Execute(t.Context(), pulloperations.Input{
		UserID:        f.reader,
		AfterPosition: 2,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Operations) != 0 || output.HasMore {
		t.Fatalf("the page carries %d changes, want none", len(output.Operations))
	}

	if output.LastPosition != 2 {
		t.Errorf("the cursor came back as %d, want the caller's own %d", output.LastPosition, 2)
	}
}

// The reply carries the cursor to continue from, so a page smaller than was
// asked for costs a round trip and never a change.
func TestExecuteClampsWhatTheCallerAskedFor(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.append(t, 3)

	for name, limit := range map[string]int{
		"a caller that asked for nothing":                          0,
		"a caller that asked for a negative page":                  -1,
		"a caller that asked for more than the node will assemble": operation.MaxPageSize * 2,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			output, err := f.usecase.Execute(t.Context(), pulloperations.Input{
				UserID: f.reader,
				Limit:  limit,
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if len(output.Operations) != 3 {
				t.Errorf("the page carries %d changes, want the whole log", len(output.Operations))
			}
		})
	}
}

// The log is one reader's, and the column every statement filters on is what
// makes that true.
func TestExecuteReadsOnlyTheReadersOwnLog(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.append(t, 2)

	output, err := f.usecase.Execute(t.Context(), pulloperations.Input{UserID: uuid.New()})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Operations) != 0 {
		t.Errorf("another reader's log came back with %d changes in it", len(output.Operations))
	}
}

func TestExecutePassesOnAFailureOfTheNode(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.log.Err = errs.New(errs.KindUnavailable, "the log is not answering")

	if _, err := f.usecase.Execute(t.Context(), pulloperations.Input{UserID: f.reader}); err == nil {
		t.Error("Execute answered with a page from a node that could not answer")
	}
}
