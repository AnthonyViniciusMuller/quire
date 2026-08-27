package deleteebook_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/deleteebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// imported is when the work below entered the collection, and digest is a
// well-formed content hash.
var imported = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase     *deleteebook.DeleteEbook
	works       *apptest.EbookRepository
	filings     *apptest.MembershipRepository
	transaction *apptest.Transaction
	reader      uuid.UUID
	phone       uuid.UUID
	work        *ebook.Ebook
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	filings := apptest.NewMembershipRepository()
	works := apptest.NewEbookRepository(filings)
	transaction := apptest.NewTransaction()
	reader, phone := uuid.New(), uuid.New()

	work, err := ebook.New(reader,
		&ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024},
		phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase: deleteebook.New(works, filings,
			apptest.NewClock(imported.Add(time.Hour)), transaction),
		works:       works,
		filings:     filings,
		transaction: transaction,
		reader:      reader,
		phone:       phone,
		work:        work,
	}
}

// file puts the work on a shelf, so that the deletion has something to clear.
func (f *fixture) file(t *testing.T, grouping uuid.UUID) {
	t.Helper()

	filing, err := membership.New(f.work.ID, grouping, f.phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.filings.Create(t.Context(), filing); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestExecuteTombstonesRatherThanRemoving(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), deleteebook.Input{
		UserID: f.reader, DeviceID: f.phone, EbookID: f.work.ID,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("the row went, so the next node that had not heard about the deletion "+
			"would resurrect it: %v", err)
	}

	switch {
	case !stored.IsDeleted():
		t.Error("the work was not marked removed")
	case stored.Revision.DeviceID != f.phone:
		t.Error("the tombstone does not name the device that made it")
	case !f.work.Revision.VectorClock.HappensBefore(stored.Revision.VectorClock):
		t.Error("the deletion does not causally dominate the version it removed")
	}
}

// The two tombstones replicate independently, so a node that wrote one without
// the other would show the work on a shelf it is no longer in — here until
// somebody noticed, and on every peer until they received the missing half.
func TestExecuteClearsEveryFilingInTheSameUnitOfWork(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.file(t, uuid.New())
	f.file(t, uuid.New())

	if _, err := f.usecase.Execute(t.Context(), deleteebook.Input{
		UserID: f.reader, DeviceID: f.phone, EbookID: f.work.ID,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if filed := f.filings.Filed(); len(filed) != 0 {
		t.Errorf("the work is still on %d shelves", len(filed))
	}

	if f.transaction.Calls() != 1 {
		t.Errorf("the two writes were made in %d units of work, want one", f.transaction.Calls())
	}
}

// A second deletion has nothing to tell the reader that the first did not, and
// stamping it again would claim a write that was not made.
func TestExecuteAnswersASecondDeletionAsAWorkThatIsNotThere(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	input := deleteebook.Input{UserID: f.reader, DeviceID: f.phone, EbookID: f.work.ID}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	first, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if _, err = f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindNotFound) {
		t.Fatalf("Execute = %v, want a not found", err)
	}

	second, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if !second.Revision.VectorClock.Equal(first.Revision.VectorClock) {
		t.Error("the refused deletion stamped a second revision anyway")
	}
}

func TestExecuteRefusesAWorkThatIsNotTheirs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), deleteebook.Input{
		UserID: uuid.New(), DeviceID: f.phone, EbookID: f.work.ID,
	})

	if !errors.Is(err, errs.KindNotFound) {
		t.Fatalf("Execute = %v, want a not found", err)
	}

	stored, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.IsDeleted() {
		t.Error("a stranger's deletion took effect")
	}
}

// The bytes are keyed by their digest and shared: another reader on this node
// may hold the same work, and a second device of this reader will ask for it
// again once the deletion has reached it and been undone.
func TestExecuteDoesNotTouchTheFile(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), deleteebook.Input{
		UserID: f.reader, DeviceID: f.phone, EbookID: f.work.ID,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.Hash != digest {
		t.Error("the tombstone forgot which file the work was")
	}
}
