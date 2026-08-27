package crdt_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestFirstVersionCountsOneEventOfTheWritingDevice(t *testing.T) {
	t.Parallel()

	phone := uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	version := crdt.FirstVersion(phone, at)

	if got := version.VectorClock.Get(crdt.Author(phone)); got != 1 {
		t.Errorf("the first write counts %d events of the writing device, want 1", got)
	}

	if version.VectorClock.Len() != 1 {
		t.Errorf("the clock is %s, want the writing device alone", version.VectorClock)
	}

	if !version.UpdatedAt.Equal(at) {
		t.Errorf("the write was stamped %s, want %s", version.UpdatedAt, at)
	}
}

// A progress row has one writer, so its versions are totally ordered by that
// device's own counter. This is the property C05 rests on, and it is the
// reason the type carries no tie-break.
func TestNextIsAlwaysCausallyAfterWhatItFollows(t *testing.T) {
	t.Parallel()

	phone := uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	first := crdt.FirstVersion(phone, at)
	second := first.Next(phone, at.Add(time.Minute))

	if !first.VectorClock.HappensBefore(second.VectorClock) {
		t.Errorf("%s does not happen before %s", first.VectorClock, second.VectorClock)
	}

	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("the second write is stamped %s, which is not after %s",
			second.UpdatedAt, first.UpdatedAt)
	}
}

// The device's clock is not to be trusted to move forward, and a causally
// later version carrying an earlier instant would let replication order the
// two the wrong way round. This is the per-record half of C01, and it holds
// on this type for the same reason it holds on a revision.
func TestNextStepsPastAClockThatRanBackwards(t *testing.T) {
	t.Parallel()

	phone := uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	first := crdt.FirstVersion(phone, at)
	second := first.Next(phone, at.Add(-time.Hour))

	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("a write made while the clock read %s was stamped %s, which is not after %s",
			at.Add(-time.Hour), second.UpdatedAt, first.UpdatedAt)
	}

	if got := second.UpdatedAt.Sub(first.UpdatedAt); got != crdt.Resolution {
		t.Errorf("the step is %s, want the smallest difference the column can represent", got)
	}
}

// The value has to survive a write followed by a read, and the column keeps
// microseconds.
func TestVersionIsStampedAtTheResolutionTheColumnKeeps(t *testing.T) {
	t.Parallel()

	phone := uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 123456789, time.UTC)

	version := crdt.FirstVersion(phone, at)

	if version.UpdatedAt.Nanosecond()%int(crdt.Resolution) != 0 {
		t.Errorf("the write was stamped %s, which a timestamptz cannot hold", version.UpdatedAt)
	}
}

func TestVersionIsZero(t *testing.T) {
	t.Parallel()

	if !(crdt.Version{}).IsZero() {
		t.Error("a version nothing has stamped reports a causal history")
	}

	if crdt.FirstVersion(uuid.New(), time.Now()).IsZero() {
		t.Error("a version a device has written reports none")
	}
}
