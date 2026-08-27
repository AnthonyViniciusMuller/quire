package progress_test

import (
	"errors"
	"math"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// read is when the positions below were reached.
var read = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// at is a well-formed position, forty per cent of the way through.
func at(t *testing.T) *progress.Position {
	t.Helper()

	percent, err := progress.NewPercent(40)
	if err != nil {
		t.Fatalf("NewPercent: %v", err)
	}

	return &progress.Position{Locator: locator.Locator("page=42"), Percent: percent}
}

func TestNew(t *testing.T) {
	t.Parallel()

	work, phone := uuid.New(), uuid.New()

	position, err := progress.New(work, phone, at(t), read)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case position.ID == (uuid.UUID{}):
		t.Error("the position was recorded without an identifier the operation could name")
	case position.EbookID != work:
		t.Error("the position does not name the work it is in")
	case !position.BelongsTo(phone):
		t.Error("the position does not name the device it belongs to")
	case position.Version.VectorClock.Get(crdt.Author(phone)) != 1:
		t.Error("the causal history does not count the write as an event of the reading device")
	case !position.Version.UpdatedAt.Equal(read):
		t.Errorf("the position was stamped %s, want %s", position.Version.UpdatedAt, read)
	}
}

func TestNewRefusesWhatCannotBeAPosition(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		work, device uuid.UUID
		locator      locator.Locator
		instant      time.Time
	}{
		"no work":     {uuid.UUID{}, uuid.New(), "page=42", read},
		"no device":   {uuid.New(), uuid.UUID{}, "page=42", read},
		"no place":    {uuid.New(), uuid.New(), "", read},
		"no instant":  {uuid.New(), uuid.New(), "page=42", time.Time{}},
		"only spaces": {uuid.New(), uuid.New(), "   ", read},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			position := &progress.Position{Locator: test.locator}

			_, err := progress.New(test.work, test.device, position, test.instant)
			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument", err)
			}
		})
	}
}

// C05: the row has one writer, so its versions are totally ordered by that
// device's own counter and two of them can never be concurrent.
func TestMoveToKeepsTheRowTotallyOrdered(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	position, err := progress.New(uuid.New(), phone, at(t), read)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := position.Version

	moved := &progress.Position{Locator: locator.Locator("page=99"), Percent: progress.NoPercent()}
	if err = position.MoveTo(moved, read.Add(time.Hour)); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}

	switch {
	case position.Locator != moved.Locator:
		t.Errorf("the reader is at %q", position.Locator)
	case position.Percent.IsKnown():
		t.Error("a client that computed no proportion had the previous one kept for it")
	case !before.VectorClock.HappensBefore(position.Version.VectorClock):
		t.Errorf("%s does not happen before %s", before.VectorClock, position.Version.VectorClock)
	case position.Version.VectorClock.Len() != 1:
		t.Errorf("the clock is %s, want the one device that may write the row",
			position.Version.VectorClock)
	case position.Version.VectorClock.Get(crdt.Author(phone)) != 2:
		t.Error("the move was not counted as an event of the device that made it")
	}
}

// The reader has read on, whatever the locator says, and a write that recorded
// nothing would leave a replica believing this device had stopped where it had
// not.
func TestMoveToStampsAWriteThatChangedNothing(t *testing.T) {
	t.Parallel()

	position, err := progress.New(uuid.New(), uuid.New(), at(t), read)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = position.MoveTo(at(t), read.Add(time.Minute)); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}

	if !position.Version.UpdatedAt.After(read) {
		t.Error("a move to the same place stamped no write")
	}
}

func TestMoveToRefusesWhatCannotBeAPosition(t *testing.T) {
	t.Parallel()

	position, err := progress.New(uuid.New(), uuid.New(), at(t), read)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	nowhere := &progress.Position{Locator: ""}
	if err = position.MoveTo(nowhere, read); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("MoveTo = %v, want an invalid argument", err)
	}

	if err = position.MoveTo(at(t), time.Time{}); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("MoveTo without an instant = %v, want an invalid argument", err)
	}

	if position.Locator != "page=42" {
		t.Errorf("a refused move left the reader at %q", position.Locator)
	}
}

// Absence and zero are different claims: a reader who has opened a work and
// read nothing is at zero, and a client that cannot compute a proportion sends
// nothing.
func TestPercentTellsAbsenceFromZero(t *testing.T) {
	t.Parallel()

	zero, err := progress.NewPercent(0)
	if err != nil {
		t.Fatalf("NewPercent: %v", err)
	}

	if !zero.IsKnown() {
		t.Error("a reader at the very start of a work was recorded as not having said where")
	}

	if progress.NoPercent().IsKnown() {
		t.Error("a client that computed nothing was recorded as having said zero")
	}
}

// reading.progress_percent_range admits 0 to 100 and nothing else; a NaN is
// refused here and not there, because both of the constraint's comparisons are
// false for one.
func TestPercentRefusesWhatTheColumnRefuses(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{-0.01, 100.01, math.NaN(), math.Inf(1)} {
		if _, err := progress.NewPercent(value); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("NewPercent(%v) = %v, want an invalid argument", value, err)
		}
	}
}

// numeric(5, 2) keeps two decimal places, so a value the database would round
// is rounded before it is stored — or what a client is told it stored is not
// what a later read returns.
func TestPercentIsRoundedToWhatTheColumnKeeps(t *testing.T) {
	t.Parallel()

	percent, err := progress.NewPercent(40.126)
	if err != nil {
		t.Fatalf("NewPercent: %v", err)
	}

	if percent.Float64() != 40.13 {
		t.Errorf("NewPercent(40.126) = %v, want 40.13", percent.Float64())
	}
}

func TestParsePercentReadsAClientThatSaidNothing(t *testing.T) {
	t.Parallel()

	percent, err := progress.ParsePercent(nil)
	if err != nil {
		t.Fatalf("ParsePercent: %v", err)
	}

	if percent.IsKnown() {
		t.Error("a client that sent no proportion was recorded as having sent one")
	}
}

func TestRestoreKeepsTheIdentifier(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	props := progress.Props{EbookID: uuid.New(), DeviceID: uuid.New()}

	if got := progress.Restore(id, &props); got.ID != id {
		t.Errorf("Restore minted %s in place of %s", got.ID, id)
	}
}
