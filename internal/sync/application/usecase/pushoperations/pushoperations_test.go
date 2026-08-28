package pushoperations_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// authored is when the changes below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *pushoperations.PushOperations
	log     *apptest.OperationRepository
	records *apptest.Records
	clock   *apptest.Clock
	tx      *apptest.Transaction
	changes *apptest.Changes

	reader uuid.UUID
	phone  uuid.UUID
	tablet uuid.UUID
}

func newFixture() *fixture {
	log := apptest.NewOperationRepository()
	records := apptest.NewRecords()
	clock := apptest.NewClock(authored)
	tx := apptest.NewTransaction(log)
	changes := apptest.NewChanges()

	return &fixture{
		usecase: pushoperations.New(log, records, clock, tx, changes),
		log:     log,
		records: records,
		clock:   clock,
		tx:      tx,
		changes: changes,
		reader:  uuid.New(),
		phone:   uuid.New(),
		tablet:  uuid.New(),
	}
}

// change is a well-formed change authored by device.
func (f *fixture) change(device uuid.UUID, counter uint64) pushoperations.Change {
	return pushoperations.Change{
		ID:           uuid.New(),
		DeviceID:     device,
		TargetEntity: "ebook",
		TargetID:     uuid.New(),
		Kind:         "update",
		Delta:        operation.Delta{"title": json.RawMessage(`"Vidas Secas"`)},
		VectorClock:  crdt.VectorClock{crdt.Author(device): counter},
		CreatedAt:    authored,
	}
}

// input is a batch the phone authored.
func (f *fixture) input(changes ...pushoperations.Change) pushoperations.Input {
	return pushoperations.Input{UserID: f.reader, Author: f.phone, Operations: changes}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()
	first, second := f.change(f.phone, 1), f.change(f.phone, 2)

	output, err := f.usecase.Execute(t.Context(), f.input(first, second))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Results) != 2 {
		t.Fatalf("the reply carries %d verdicts, want one per change offered", len(output.Results))
	}

	for index, result := range output.Results {
		if result.Outcome != operation.OutcomeApplied {
			t.Errorf("the verdict on change %d is %s (%s), want applied",
				index, result.Outcome, result.Detail)
		}
	}

	switch {
	case output.Results[0].OperationID != first.ID || output.Results[1].OperationID != second.ID:
		t.Error("the verdicts are not in the order the changes were offered, so nothing can be matched")
	case output.LastPosition != 2:
		t.Errorf("the log ends at %d, want the two changes numbered", output.LastPosition)
	case f.log.Len() != 2:
		t.Errorf("the log holds %d changes", f.log.Len())
	}
}

// An operation reaching a node twice by two routes is the normal shape of a
// federation, and it is recognized by the identifier its author minted rather
// than by comparing payloads.
func TestExecuteReportsAChangeThisNodeAlreadyHadAsDuplicate(t *testing.T) {
	t.Parallel()

	f := newFixture()
	change := f.change(f.phone, 1)

	if _, err := f.usecase.Execute(t.Context(), f.input(change)); err != nil {
		t.Fatalf("the first push: %v", err)
	}

	output, err := f.usecase.Execute(t.Context(), f.input(change))
	if err != nil {
		t.Fatalf("the second push: %v", err)
	}

	if output.Results[0].Outcome != operation.OutcomeDuplicate {
		t.Errorf("the second offer = %s, want duplicate", output.Results[0].Outcome)
	}

	if seen := f.records.Seen(); len(seen) != 1 {
		t.Errorf("the reconciler was asked about the change %d times, want once", len(seen))
	}
}

// The contract stores what was superseded, because a later node has to reach
// the same conclusion from the same history.
func TestExecuteKeepsASupersededChangeInTheLog(t *testing.T) {
	t.Parallel()

	f := newFixture()
	change := f.change(f.phone, 1)
	f.records.Answer(change.ID, operation.Superseded())

	output, err := f.usecase.Execute(t.Context(), f.input(change))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Results[0].Outcome != operation.OutcomeSuperseded {
		t.Fatalf("the verdict is %s, want superseded", output.Results[0].Outcome)
	}

	if !f.log.Holds(change.ID) {
		t.Error("a superseded change was dropped, so a later node could not reach the same conclusion")
	}
}

// And stores nothing it refused. The unit of work is one change for this
// reason: a rejection has to leave nothing behind while the changes around it
// stand.
func TestExecuteStoresNothingItRefused(t *testing.T) {
	t.Parallel()

	f := newFixture()
	refused, accepted := f.change(f.phone, 1), f.change(f.phone, 2)
	f.records.Answer(refused.ID, operation.Rejected("no such work here"))

	output, err := f.usecase.Execute(t.Context(), f.input(refused, accepted))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Results[0].Outcome != operation.OutcomeRejected:
		t.Errorf("the refused change = %s, want rejected", output.Results[0].Outcome)
	case output.Results[0].Detail != "no such work here":
		t.Errorf("the rejection says %q, and the operator has nothing to act on", output.Results[0].Detail)
	case output.Results[1].Outcome != operation.OutcomeApplied:
		t.Error("one refused change took the rest of the batch with it")
	case f.log.Holds(refused.ID):
		t.Error("a refused change was stored anyway")
	case !f.log.Holds(accepted.ID):
		t.Error("the change beside it was rolled back with it")
	}
}

// RN10: a batch with a forged author in it is not a batch any of which should
// be trusted, so it fails whole rather than per change.
func TestExecuteRefusesABatchWithAChangeTheCallerDidNotAuthor(t *testing.T) {
	t.Parallel()

	f := newFixture()
	mine, theirs := f.change(f.phone, 1), f.change(f.tablet, 1)

	_, err := f.usecase.Execute(t.Context(), f.input(mine, theirs))
	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Fatalf("Execute = %v, want a permission denied", err)
	}

	if f.log.Len() != 0 {
		t.Error("the batch was refused after part of it had already been stored")
	}
}

// A peer node replicates many devices and is none of them, so there is nothing
// for RN10 to check. What authorizes that call is the reader's own permission
// for the node, which its controller established before this was reached.
func TestExecuteAcceptsAPeersBatchAuthoredByAnyDevice(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), pushoperations.Input{
		UserID:     f.reader,
		Operations: []pushoperations.Change{f.change(f.phone, 1), f.change(f.tablet, 1)},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for index, result := range output.Results {
		if result.Outcome != operation.OutcomeApplied {
			t.Errorf("change %d = %s, want applied", index, result.Outcome)
		}
	}
}

// A malformed change is one rejection and not the end of the batch: what RN10
// refuses whole is a forged author, which is a claim about the caller, and a
// delta this node cannot read is a claim about one change.
func TestExecuteRejectsAMalformedChangeWithoutEndingTheBatch(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*pushoperations.Change){
		"an entity this node does not replicate": func(c *pushoperations.Change) {
			c.TargetEntity = "shelf"
		},
		"a kind of change this node cannot name": func(c *pushoperations.Change) { c.Kind = "upsert" },
		"a change that names no record":          func(c *pushoperations.Change) { c.TargetID = uuid.UUID{} },
		"a history that does not count its author": func(c *pushoperations.Change) {
			c.VectorClock = crdt.VectorClock{}
		},
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			broken, sound := f.change(f.phone, 1), f.change(f.phone, 2)
			breaks(&broken)

			output, err := f.usecase.Execute(t.Context(), f.input(broken, sound))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if output.Results[0].Outcome != operation.OutcomeRejected {
				t.Errorf("the malformed change = %s, want rejected", output.Results[0].Outcome)
			}

			if output.Results[1].Outcome != operation.OutcomeApplied {
				t.Error("a malformed change took the rest of the batch with it")
			}
		})
	}
}

// Every instant the node is told about has to reach the clock, or a local
// write made afterwards could be stamped before it and C01's bad edge is back.
func TestExecuteTellsTheClockAboutEveryInstantItWasOffered(t *testing.T) {
	t.Parallel()

	f := newFixture()
	change := f.change(f.phone, 1)
	change.CreatedAt = authored.Add(time.Hour)

	if _, err := f.usecase.Execute(t.Context(), f.input(change)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	observed := f.clock.Observed()
	if len(observed) != 1 || !observed[0].Equal(change.CreatedAt) {
		t.Fatalf("the clock was told about %v, want the instant the change carried", observed)
	}

	// And a duplicate is still an instant this node has been told about.
	if _, err := f.usecase.Execute(t.Context(), f.input(change)); err != nil {
		t.Fatalf("the second push: %v", err)
	}

	if observed = f.clock.Observed(); len(observed) != 2 {
		t.Errorf("the clock was told about %d instants, want the duplicate counted too", len(observed))
	}
}

// The node failing is not a verdict. A batch reported as refused because the
// database went away would be lost rather than retried.
func TestExecutePassesOnAFailureOfTheNode(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.records.Err = errs.New(errs.KindUnavailable, "the catalogue is not answering")

	if _, err := f.usecase.Execute(t.Context(), f.input(f.change(f.phone, 1))); err == nil {
		t.Error("Execute answered with verdicts on a node that could not answer")
	}
}

// A push of nothing is a question about where the log ends, and a device that
// has just reconnected asks it.
func TestExecuteAnswersAnEmptyBatchWithTheHeadOfTheLog(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if _, err := f.usecase.Execute(t.Context(), f.input(f.change(f.phone, 1))); err != nil {
		t.Fatalf("the first push: %v", err)
	}

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Results) != 0 || output.LastPosition != 1 {
		t.Errorf("Execute = %+v, want no verdicts and the head of the log", output)
	}
}

// A change made on one device has to reach the reader's other devices as it
// happens rather than at the next poll, and the only call that knows the log
// grew is the one that grew it.
func TestExecuteWakesTheReadersOpenStreams(t *testing.T) {
	t.Parallel()

	f := newFixture()
	change := f.change(f.phone, 1)

	if _, err := f.usecase.Execute(t.Context(), f.input(change)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if announced := f.changes.Announced(); len(announced) != 1 || announced[0] != f.reader {
		t.Fatalf("the hub was told about %v, want the reader whose log grew", announced)
	}

	// A batch that grew nothing wakes nobody: every change in it was already
	// here, so a stream that looked would find what it already has.
	if _, err := f.usecase.Execute(t.Context(), f.input(change)); err != nil {
		t.Fatalf("the second push: %v", err)
	}

	if announced := f.changes.Announced(); len(announced) != 1 {
		t.Errorf("the hub was told about %d pushes, want only the one that grew the log", len(announced))
	}
}

// A superseded change grew the log as surely as an applied one did: it is
// stored, it has a position, and a device pulling from here has to be given it.
func TestExecuteWakesTheStreamsForASupersededChangeToo(t *testing.T) {
	t.Parallel()

	f := newFixture()
	change := f.change(f.phone, 1)
	f.records.Answer(change.ID, operation.Superseded())

	if _, err := f.usecase.Execute(t.Context(), f.input(change)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if announced := f.changes.Announced(); len(announced) != 1 {
		t.Errorf("the hub was told about %d pushes, want the superseded change counted", len(announced))
	}
}
