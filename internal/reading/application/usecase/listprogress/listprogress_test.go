package listprogress_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// read is when the positions below were reached.
var read = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase   *listprogress.ListProgress
	positions *apptest.ProgressRepository
	works     *apptest.Works
	reader    uuid.UUID
	work      uuid.UUID
}

func newFixture() *fixture {
	positions := apptest.NewProgressRepository()
	works := apptest.NewWorks()
	f := &fixture{
		usecase:   listprogress.New(positions, works),
		positions: positions,
		works:     works,
		reader:    uuid.New(),
		work:      uuid.New(),
	}

	works.Add(f.work, f.reader)

	return f
}

// stopped records where one device stopped in the work.
func (f *fixture) stopped(t *testing.T, work, device uuid.UUID, at string) {
	t.Helper()

	position, err := progress.New(work, device,
		&progress.Position{Locator: locator.Locator(at), Percent: progress.NoPercent()}, read)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = f.positions.Create(t.Context(), position); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// RN01 says a reader resumes where they stopped, and on a reader with three
// appliances there are three answers to that. Which one to show is the client's
// decision, so the call does not make it by returning one row.
func TestExecuteReturnsEveryDevicesPosition(t *testing.T) {
	t.Parallel()

	f := newFixture()
	devices := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	for index, device := range devices {
		f.stopped(t, f.work, device, "page="+string(rune('a'+index)))
	}

	output, err := f.usecase.Execute(t.Context(),
		listprogress.Input{UserID: f.reader, EbookID: f.work})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Progress) != len(devices) {
		t.Fatalf("the reply holds %d positions, want one per device", len(output.Progress))
	}

	// Ordered by device, so that two calls return the same list in the same
	// order — which a client diffing against what it already showed depends on.
	for index := 1; index < len(output.Progress); index++ {
		if output.Progress[index-1].DeviceID.String() > output.Progress[index].DeviceID.String() {
			t.Error("the positions came back in an order a second call could disagree with")
		}
	}
}

// A work the reader has not started is an empty reply and not a refusal: they
// may see the work, they simply have not opened it.
func TestExecuteOfAWorkNobodyHasStarted(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(),
		listprogress.Input{UserID: f.reader, EbookID: f.work})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Progress) != 0 {
		t.Errorf("the reply holds %d positions", len(output.Progress))
	}
}

// The rows are scoped by the work, so a work belonging to somebody else would
// return where that reader had stopped in it.
func TestExecuteRefusesAWorkTheReaderMayNotSee(t *testing.T) {
	t.Parallel()

	f := newFixture()
	somebodyElse := uuid.New()
	f.works.Add(somebodyElse, uuid.New())
	f.stopped(t, somebodyElse, uuid.New(), "page=1")

	_, err := f.usecase.Execute(t.Context(),
		listprogress.Input{UserID: f.reader, EbookID: somebodyElse})
	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("Execute = %v, want a not found", err)
	}
}
