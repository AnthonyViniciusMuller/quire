package updateprogress_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// read is when the positions below were reached.
var read = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase   *updateprogress.UpdateProgress
	positions *apptest.ProgressRepository
	works     *apptest.Works
	clock     *apptest.Clock
	reader    uuid.UUID
	phone     uuid.UUID
	tablet    uuid.UUID
	work      uuid.UUID
}

func newFixture() *fixture {
	positions := apptest.NewProgressRepository()
	works := apptest.NewWorks()
	clock := apptest.NewClock(read)
	f := &fixture{
		usecase:   updateprogress.New(positions, works, clock),
		positions: positions,
		works:     works,
		clock:     clock,
		reader:    uuid.New(),
		phone:     uuid.New(),
		tablet:    uuid.New(),
		work:      uuid.New(),
	}

	works.Add(f.work, f.reader)

	return f
}

// input is a well-formed position, forty per cent of the way through.
func (f *fixture) input() updateprogress.Input {
	percent := 40.0

	return updateprogress.Input{
		UserID:   f.reader,
		DeviceID: f.phone,
		EbookID:  f.work,
		Locator:  "page=42",
		Percent:  &percent,
	}
}

func TestExecuteRecordsTheFirstPosition(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	position := output.Progress

	switch {
	case position.EbookID != f.work || !position.BelongsTo(f.phone):
		t.Error("the position does not name the pair that addresses it")
	case position.Locator.String() != "page=42":
		t.Errorf("the reader is at %q", position.Locator)
	case !position.Percent.IsKnown() || position.Percent.Float64() != 40:
		t.Errorf("the proportion is %+v", position.Percent)
	case position.Version.VectorClock.Get(crdt.Author(f.phone)) != 1:
		t.Error("the causal history does not count the write as an event of the reading device")
	case !position.Version.UpdatedAt.Equal(read):
		t.Errorf("the position was stamped %s, want the clock's %s", position.Version.UpdatedAt, read)
	}
}

// One row per work and device, overwritten rather than accumulated — C05, the
// constraint Quadro 21 does not have. Without it "where this device stopped in
// this book" stops having one answer.
func TestExecuteOverwritesTheDevicesOwnRow(t *testing.T) {
	t.Parallel()

	f := newFixture()

	first, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	f.clock.Advance(time.Hour)

	moved := f.input()
	moved.Locator = "page=99"
	moved.Percent = nil

	second, err := f.usecase.Execute(t.Context(), moved)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case second.Progress.ID != first.Progress.ID:
		t.Error("the second call recorded a second row rather than moving the first")
	case second.Progress.Locator.String() != "page=99":
		t.Errorf("the reader is at %q", second.Progress.Locator)
	case second.Progress.Percent.IsKnown():
		t.Error("a client that computed no proportion had the previous one kept for it")
	case second.Progress.Version.VectorClock.Get(crdt.Author(f.phone)) != 2:
		t.Error("the move was not counted as an event of the device that made it")
	case !second.Progress.Version.UpdatedAt.After(first.Progress.Version.UpdatedAt):
		t.Error("the move was not stamped after the write it follows")
	}

	rows, err := f.positions.ListForEbook(t.Context(), f.work)
	if err != nil {
		t.Fatalf("ListForEbook: %v", err)
	}

	if len(rows) != 1 {
		t.Errorf("the work holds %d positions for one device", len(rows))
	}
}

// A device writes its own position and no other device's, which RN10 requires
// of every operation and which here is also what makes the entity
// conflict-free. Two devices are two rows, never a conflict.
func TestExecuteKeepsEachDevicesPositionApart(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	fromTablet := f.input()
	fromTablet.DeviceID = f.tablet
	fromTablet.Locator = "page=7"

	if _, err := f.usecase.Execute(t.Context(), fromTablet); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rows, err := f.positions.ListForEbook(t.Context(), f.work)
	if err != nil {
		t.Fatalf("ListForEbook: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("the work holds %d positions, want one per device", len(rows))
	}

	for _, row := range rows {
		if row.Version.VectorClock.Len() != 1 {
			t.Errorf("the clock of %s is %s, want the one device that may write the row",
				row.DeviceID, row.Version.VectorClock)
		}
	}
}

// Two calls from the same device crossing on a flaky network is the ordinary
// way the pair constraint is broken, and the loser reads the row the winner
// wrote and moves it rather than failing.
func TestExecuteAnswersACrossingCallByMovingTheRowItFound(t *testing.T) {
	t.Parallel()

	f := newFixture()

	// The row a call from the same device wrote while this one was between its
	// read and its write, which is what a lost reply and a retry look like.
	f.positions.BeforeCreate = func() {
		winner, err := progress.New(f.work, f.phone, &progress.Position{
			Locator: locator.Locator("page=1"),
			Percent: progress.NoPercent(),
		}, read)
		if err != nil {
			t.Errorf("New: %v", err)

			return
		}

		if err = f.positions.Create(t.Context(), winner); err != nil {
			t.Errorf("Create: %v", err)
		}
	}

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rows, err := f.positions.ListForEbook(t.Context(), f.work)
	if err != nil {
		t.Fatalf("ListForEbook: %v", err)
	}

	switch {
	case len(rows) != 1:
		t.Errorf("the work holds %d positions for one device", len(rows))
	case output.Progress.Locator.String() != "page=42":
		t.Errorf("the reader is at %q, want where this call said", output.Progress.Locator)
	case output.Progress.Version.VectorClock.Get(crdt.Author(f.phone)) != 2:
		t.Error("the retry did not build on the version the crossing call wrote")
	}
}

func TestExecuteRefusesAWorkTheReaderMayNotSee(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*fixture, *updateprogress.Input){
		"a work that does not exist": func(_ *fixture, in *updateprogress.Input) {
			in.EbookID = uuid.New()
		},
		"a work that was tombstoned": func(f *fixture, _ *updateprogress.Input) {
			f.works.Remove(f.work)
		},
		"a work belonging to somebody else": func(_ *fixture, in *updateprogress.Input) {
			in.UserID = uuid.New()
		},
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			input := f.input()
			breaks(f, &input)

			if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindNotFound) {
				t.Errorf("Execute = %v, want a not found", err)
			}
		})
	}
}

func TestExecuteRefusesWhatCannotBeAPosition(t *testing.T) {
	t.Parallel()

	tooFar := 100.01

	tests := map[string]func(*updateprogress.Input){
		"no place":                  func(in *updateprogress.Input) { in.Locator = "  " },
		"a proportion out of range": func(in *updateprogress.Input) { in.Percent = &tooFar },
		"no device":                 func(in *updateprogress.Input) { in.DeviceID = uuid.UUID{} },
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			input := f.input()
			breaks(&input)

			if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Execute = %v, want an invalid argument", err)
			}
		})
	}
}
