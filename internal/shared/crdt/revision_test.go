package crdt_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// wrote is the instant the revisions below were stamped at.
var wrote = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func TestFirstRevisionCountsOneEventOfItsAuthor(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	revision := crdt.FirstRevision(phone, wrote)

	switch {
	case revision.VectorClock.Get(crdt.Author(phone)) != 1:
		t.Errorf("the author observed %d of its own events, want 1",
			revision.VectorClock.Get(crdt.Author(phone)))
	case revision.VectorClock.Len() != 1:
		t.Errorf("the clock names %d devices, want only the author", revision.VectorClock.Len())
	case revision.DeviceID != phone:
		t.Error("the revision does not name the device whose write it reflects")
	case !revision.UpdatedAt.Equal(wrote):
		t.Errorf("the revision was stamped %s, want %s", revision.UpdatedAt, wrote)
	case revision.Deleted:
		t.Error("a record that was just created is a tombstone")
	}
}

func TestNextDominatesTheVersionItDerivesFrom(t *testing.T) {
	t.Parallel()

	phone, tablet := uuid.New(), uuid.New()

	first := crdt.FirstRevision(phone, wrote)
	second := first.Next(tablet, wrote.Add(time.Second))

	switch {
	case !first.VectorClock.HappensBefore(second.VectorClock):
		t.Error("the later version does not causally dominate the earlier one")
	case second.DeviceID != tablet:
		t.Error("the revision names the device that created the record rather than the one that wrote it")
	case second.VectorClock.Get(crdt.Author(phone)) != 1:
		t.Error("the write dropped the event the other device had authored")
	}
}

// The rule C01 states, applied to one record: a second write is later than the
// first even when the authoring device's clock ran backwards between them.
// Without it the tie-break has a cycle and merge stops being associative.
func TestNextStampsLaterThanTheVersionItDerivesFrom(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	first := crdt.FirstRevision(phone, wrote)
	second := first.Next(phone, wrote.Add(-time.Hour))

	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("a write from a device whose clock ran backwards was stamped %s, "+
			"which is not after the %s it derives from", second.UpdatedAt, first.UpdatedAt)
	}

	if step := second.UpdatedAt.Sub(first.UpdatedAt); step != crdt.Resolution {
		t.Errorf("the write stepped by %s, want the %s a timestamptz can still tell apart",
			step, crdt.Resolution)
	}
}

// A value the database cannot store is a value that changes when it is read
// back, and a reconciler decides conflicts by comparing exactly these.
func TestStampingTruncatesToWhatTheColumnHolds(t *testing.T) {
	t.Parallel()

	revision := crdt.FirstRevision(uuid.New(), wrote.Add(1500*time.Nanosecond))

	if remainder := revision.UpdatedAt.Sub(revision.UpdatedAt.Truncate(crdt.Resolution)); remainder != 0 {
		t.Errorf("the revision was stamped with %s of resolution the column cannot hold", remainder)
	}
}

func TestTombstoneIsAWriteLikeAnyOther(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	present := crdt.FirstRevision(phone, wrote)
	removed := present.Tombstone(phone, wrote.Add(time.Second))

	switch {
	case !removed.Deleted:
		t.Error("the record was not marked removed")
	case !present.VectorClock.HappensBefore(removed.VectorClock):
		t.Error("the deletion does not causally dominate the version it removed, so a node that " +
			"had not heard about it would resurrect the record")
	}

	restored := removed.Restore(phone, wrote.Add(2*time.Second))
	if restored.Deleted {
		t.Error("the record stayed removed after a write that says otherwise")
	}
}

// A write that follows a deletion without saying anything about it must not
// resurrect the record: only a write that claims the record is present may.
func TestNextKeepsTheTombstone(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	removed := crdt.FirstRevision(phone, wrote).Tombstone(phone, wrote.Add(time.Second))

	if !removed.Next(phone, wrote.Add(2*time.Second)).Deleted {
		t.Error("an ordinary write cleared the tombstone")
	}
}

func TestIsZeroDescribesAValueNothingHasStamped(t *testing.T) {
	t.Parallel()

	if !(crdt.Revision{}).IsZero() {
		t.Error("the zero revision does not report itself as one")
	}

	if crdt.FirstRevision(uuid.New(), wrote).IsZero() {
		t.Error("a stamped revision reports itself as one nothing has written")
	}
}
