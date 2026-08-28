package operation_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// authored is when the operations below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is a well-formed operation and the identifiers it is built from.
type fixture struct {
	id     uuid.UUID
	reader uuid.UUID
	phone  uuid.UUID
	work   uuid.UUID
}

func newFixture() *fixture {
	return &fixture{id: uuid.New(), reader: uuid.New(), phone: uuid.New(), work: uuid.New()}
}

func (f *fixture) props() operation.Props {
	return operation.Props{
		UserID:      f.reader,
		DeviceID:    f.phone,
		Target:      operation.Target{Entity: operation.TargetEbook, ID: f.work},
		Kind:        operation.KindUpdate,
		Delta:       operation.Delta{"title": json.RawMessage(`"Vidas Secas"`)},
		VectorClock: crdt.VectorClock{}.Tick(crdt.Author(f.phone)),
		CreatedAt:   authored,
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	f := newFixture()

	props := f.props()

	op, err := operation.New(f.id, &props)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case op.ID != f.id:
		t.Error("the operation was renamed, so no node could recognize it as one it already had")
	case !op.IsAuthoredBy(f.phone):
		t.Error("the operation does not name the device that authored it, which RN10 checks a push against")
	case !op.CreatedAt.Equal(authored):
		t.Errorf("the operation was stamped %s, want the author's %s", op.CreatedAt, authored)
	case op.Author() != crdt.Author(f.phone):
		t.Error("the clock entry is not keyed by the authoring device")
	}
}

// The position is this node's to allocate and never the sender's to claim: the
// sender cannot know a number the receiver has not allocated yet, which is
// what the contract says when it declares the field ignored in a request.
func TestNewIgnoresAPositionTheSenderClaimed(t *testing.T) {
	t.Parallel()

	f := newFixture()

	props := f.props()
	props.Position = 4096

	op, err := operation.New(f.id, &props)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if op.Position != 0 {
		t.Errorf("the operation arrived at position %d, want the sender's claim dropped", op.Position)
	}

	op.PlaceAt(7)

	if op.Position != 7 {
		t.Errorf("PlaceAt left the operation at %d", op.Position)
	}
}

// Everything refused here would make the row unusable to a reconciler, which
// is a different question from what a client should have sent.
func TestNewRefusesWhatNoReconcilerCouldUse(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*fixture, *operation.Props) uuid.UUID{
		"an operation nobody can deduplicate": func(_ *fixture, _ *operation.Props) uuid.UUID {
			return uuid.UUID{}
		},
		"an operation belonging to no reader": func(f *fixture, p *operation.Props) uuid.UUID {
			p.UserID = uuid.UUID{}

			return f.id
		},
		"an operation authored by no device": func(f *fixture, p *operation.Props) uuid.UUID {
			p.DeviceID = uuid.UUID{}

			return f.id
		},
		"an operation that does not say when it was authored": func(f *fixture, p *operation.Props) uuid.UUID {
			p.CreatedAt = time.Time{}

			return f.id
		},
		"an operation naming no record": func(f *fixture, p *operation.Props) uuid.UUID {
			p.Target = operation.Target{}

			return f.id
		},
		"an operation that does not say what it did": func(f *fixture, p *operation.Props) uuid.UUID {
			p.Kind = ""

			return f.id
		},
		"an update naming no field": func(f *fixture, p *operation.Props) uuid.UUID {
			p.Delta = nil

			return f.id
		},
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			props := f.props()
			id := breaks(f, &props)

			if _, err := operation.New(id, &props); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument", err)
			}
		})
	}
}

// A clock that does not count its own author claims to have been written by a
// device that had written nothing, which makes the operation concurrent with
// the very write it was derived from — and the tie-break would then settle a
// pair causality should have ordered.
func TestNewRefusesAClockThatDoesNotCountItsAuthor(t *testing.T) {
	t.Parallel()

	f := newFixture()

	for name, clock := range map[string]crdt.VectorClock{
		"an empty history":            {},
		"somebody else's history":     {crdt.Author(uuid.New()): 3},
		"an explicit zero of its own": {crdt.Author(f.phone): 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			props := f.props()
			props.VectorClock = clock

			if _, err := operation.New(f.id, &props); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument", err)
			}
		})
	}
}

// The stored clock is canonical and the stored instant is at the resolution
// the column keeps, so an operation written and read back is the value that is
// still in memory. Two histories that are the same history must not compare
// unequal because of how they were built.
func TestNewNormalizesWhatItStores(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stranger := uuid.New()

	props := f.props()
	props.VectorClock = crdt.VectorClock{crdt.Author(f.phone): 1, crdt.Author(stranger): 0}
	props.CreatedAt = authored.Add(750 * time.Nanosecond)

	op, err := operation.New(f.id, &props)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if op.VectorClock.Len() != 1 || len(op.VectorClock) != 1 {
		t.Errorf("the stored history is %s, want the zero entry dropped", op.VectorClock)
	}

	if !op.CreatedAt.Equal(authored) {
		t.Errorf("the instant was stored as %s, want it truncated to %s", op.CreatedAt, authored)
	}
}

// Deletion is a write like any other: it carries a clock, an instant and the
// device that made it, and reconciles by the rule everything else does.
func TestRevisionCarriesTheTombstoneAsTheKindOfChange(t *testing.T) {
	t.Parallel()

	f := newFixture()

	props := f.props()
	props.Kind = operation.KindDelete
	props.Delta = nil

	op, err := operation.New(f.id, &props)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	revision := op.Revision()

	switch {
	case !revision.Deleted:
		t.Error("a deletion produced a revision that is not a tombstone")
	case revision.DeviceID != f.phone:
		t.Error("the revision does not name the device whose write the record would reflect")
	case !revision.UpdatedAt.Equal(authored):
		t.Errorf("the revision was stamped %s", revision.UpdatedAt)
	}
}

// Reading progress has exactly one writer, so its versions can never be
// concurrent and it carries the clock and the instant without the two fields
// that break a tie (C05).
func TestVersionDropsTheTieBreak(t *testing.T) {
	t.Parallel()

	f := newFixture()

	props := f.props()
	props.Target = operation.Target{Entity: operation.TargetReadingProgress, ID: uuid.New()}

	op, err := operation.New(f.id, &props)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	version := op.Version()

	if !version.VectorClock.Equal(op.VectorClock) || !version.UpdatedAt.Equal(authored) {
		t.Errorf("the version does not carry the causal state: %+v", version)
	}
}

// A row this node can no longer parse must be merely unfamiliar rather than
// unreadable: an operation naming an entity a later version added is
// replicated back here, and refusing to read it would make adding one a change
// that breaks the federation.
func TestRestoreAsksNothingOfWhatIsAlreadyStored(t *testing.T) {
	t.Parallel()

	id := uuid.New()

	op := operation.Restore(id, &operation.Props{
		Target: operation.Target{Entity: "shelf", ID: uuid.New()},
		Kind:   "reorder",
	})

	if op.ID != id || op.Target.Entity != "shelf" || op.Kind != "reorder" {
		t.Errorf("Restore changed what was stored: %+v", op)
	}
}
