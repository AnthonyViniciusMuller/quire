package deleteannotation_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/deleteannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// made is when the mark was written, and removed is when the clock stands for
// the deletion.
var (
	made    = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	removed = made.Add(time.Hour)
)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *deleteannotation.DeleteAnnotation
	marks   *apptest.AnnotationRepository
	works   *apptest.Works
	reader  uuid.UUID
	phone   uuid.UUID
	tablet  uuid.UUID
	work    uuid.UUID
}

func newFixture() *fixture {
	marks := apptest.NewAnnotationRepository()
	works := apptest.NewWorks()
	f := &fixture{
		usecase: deleteannotation.New(marks, works, apptest.NewClock(removed)),
		marks:   marks,
		works:   works,
		reader:  uuid.New(),
		phone:   uuid.New(),
		tablet:  uuid.New(),
		work:    uuid.New(),
	}

	works.Add(f.work, f.reader)

	return f
}

// note records one note made on the phone and returns it.
func (f *fixture) note(t *testing.T) *annotation.Annotation {
	t.Helper()

	mark := &annotation.Mark{
		Kind:    annotation.KindNote,
		Text:    "uma nota",
		Locator: locator.Locator("page=42"),
	}

	stored, err := annotation.New(f.work, mark, f.phone, made)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = f.marks.Create(t.Context(), stored); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return stored
}

// The row stays and the tombstone travels, or the next node that had not heard
// about the deletion resurrects the mark by replying with its own copy.
func TestExecuteTombstonesRatherThanRemoves(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)

	if _, err := f.usecase.Execute(t.Context(), deleteannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.tablet,
		AnnotationID: stored.ID,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	after, err := f.marks.GetByID(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("the row was removed rather than tombstoned: %v", err)
	}

	switch {
	case !after.IsDeleted():
		t.Error("the mark was not tombstoned")
	case after.Revision.DeviceID != f.tablet:
		t.Error("the tombstone does not name the device that made the deletion")
	case after.Revision.VectorClock.Get(crdt.Author(f.tablet)) != 1:
		t.Error("the deletion was not counted as an event of the deleting device")
	case !after.Revision.UpdatedAt.Equal(removed):
		t.Errorf("the deletion was stamped %s, want the clock's %s", after.Revision.UpdatedAt, removed)
	case after.Text.IsZero():
		t.Error("the tombstone discarded what the mark said, which a peer still has to reconcile")
	}
}

// Stamping a mark that is already gone would claim a write that was not made,
// and a second deletion has nothing to tell the reader that the first did not.
func TestExecuteRefusesAMarkThatIsAlreadyGone(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)
	input := deleteannotation.Input{UserID: f.reader, DeviceID: f.phone, AnnotationID: stored.ID}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	_, err := f.usecase.Execute(t.Context(), input)
	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("a second deletion = %v, want a not found", err)
	}

	after, err := f.marks.GetByID(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if after.Revision.VectorClock.Get(crdt.Author(f.phone)) != 2 {
		t.Error("the refused second deletion stamped a write")
	}
}

func TestExecuteRefusesAMarkTheReaderMayNotSee(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)

	_, err := f.usecase.Execute(t.Context(), deleteannotation.Input{
		UserID:       uuid.New(),
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
	})
	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("Execute = %v, want a not found", err)
	}

	after, getErr := f.marks.GetByID(t.Context(), stored.ID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}

	if after.IsDeleted() {
		t.Error("a reader who may not see the mark deleted it")
	}
}

// A library that could not be read is not a mark that does not exist: a
// client told the mark is gone would believe it and stop asking.
func TestExecuteReportsALibraryThatCouldNotBeRead(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)
	f.works.Err = errs.New(errs.KindUnavailable, "the database is unavailable")

	_, err := f.usecase.Execute(t.Context(), deleteannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
	})
	if !errors.Is(err, errs.KindUnavailable) {
		t.Errorf("Execute = %v, want the library's own failure", err)
	}
}
