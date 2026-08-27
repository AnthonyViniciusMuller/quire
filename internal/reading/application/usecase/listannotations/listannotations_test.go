package listannotations_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listannotations"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *listannotations.ListAnnotations
	marks   *apptest.AnnotationRepository
	works   *apptest.Works
	reader  uuid.UUID
	phone   uuid.UUID
	work    uuid.UUID
}

func newFixture() *fixture {
	marks := apptest.NewAnnotationRepository()
	works := apptest.NewWorks()
	f := &fixture{
		usecase: listannotations.New(marks, works),
		marks:   marks,
		works:   works,
		reader:  uuid.New(),
		phone:   uuid.New(),
		work:    uuid.New(),
	}

	works.Add(f.work, f.reader)

	return f
}

// write records count highlights in the work and returns them.
func (f *fixture) write(t *testing.T, work uuid.UUID, count int) []*annotation.Annotation {
	t.Helper()

	written := make([]*annotation.Annotation, 0, count)

	for index := range count {
		mark := &annotation.Mark{
			Kind:    annotation.KindHighlight,
			Locator: locator.Locator("page=" + string(rune('a'+index))),
		}

		stored, err := annotation.New(work, mark, f.phone, time.Now())
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = f.marks.Create(t.Context(), stored); err != nil {
			t.Fatalf("Create: %v", err)
		}

		written = append(written, stored)
	}

	return written
}

// A client that walks every page has to see every mark exactly once. That is
// the whole of what the ordering promises, and it is what a client sorting them
// into document order itself depends on.
func TestExecuteWalksEveryMarkExactlyOnce(t *testing.T) {
	t.Parallel()

	f := newFixture()
	written := f.write(t, f.work, 7)

	seen := map[uuid.UUID]int{}
	cursor := annotation.Cursor{}

	for pages := 0; ; pages++ {
		if pages > len(written) {
			t.Fatal("the walk did not terminate, so a cursor repeated a page")
		}

		output, err := f.usecase.Execute(t.Context(), listannotations.Input{
			UserID:   f.reader,
			EbookID:  f.work,
			PageSize: 2,
			Cursor:   cursor,
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}

		for _, mark := range output.Annotations {
			seen[mark.ID]++
		}

		if output.NextCursor.IsZero() {
			break
		}

		cursor = output.NextCursor
	}

	if len(seen) != len(written) {
		t.Errorf("the walk saw %d marks, want %d", len(seen), len(written))
	}

	for id, count := range seen {
		if count != 1 {
			t.Errorf("mark %s was returned %d times", id, count)
		}
	}
}

// Tombstoned marks are not listed: this call answers what the reader has
// written, not what they once wrote. The history is the sync service's.
func TestExecuteDoesNotListWhatWasDeleted(t *testing.T) {
	t.Parallel()

	f := newFixture()
	written := f.write(t, f.work, 3)

	written[0].Delete(f.phone, time.Now())

	if err := f.marks.Update(t.Context(), written[0]); err != nil {
		t.Fatalf("Update: %v", err)
	}

	output, err := f.usecase.Execute(t.Context(),
		listannotations.Input{UserID: f.reader, EbookID: f.work})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Annotations) != 2 {
		t.Errorf("the page holds %d marks, want the two that are still there", len(output.Annotations))
	}
}

// A page of works is scoped to the reader by the statement, so a grouping
// belonging to somebody else narrows it to nothing. A page of marks is scoped
// by the work, so the same omission here would return another reader's notes.
func TestExecuteRefusesAWorkTheReaderMayNotSee(t *testing.T) {
	t.Parallel()

	f := newFixture()
	somebodyElse := uuid.New()
	f.works.Add(somebodyElse, uuid.New())
	f.write(t, somebodyElse, 3)

	_, err := f.usecase.Execute(t.Context(),
		listannotations.Input{UserID: f.reader, EbookID: somebodyElse})
	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("Execute = %v, want a not found", err)
	}
}

// A client that asked for more than one reply can carry is served the ceiling
// rather than refused: it is not wrong to want every note at once, only wrong
// about what one reply holds, and the cursor is how it gets the rest.
func TestExecuteClampsThePageSize(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.write(t, f.work, 3)

	for _, asked := range []int{0, -1, annotation.MaxPageSize + 1} {
		output, err := f.usecase.Execute(t.Context(), listannotations.Input{
			UserID:   f.reader,
			EbookID:  f.work,
			PageSize: asked,
		})
		if err != nil {
			t.Fatalf("Execute with a page size of %d: %v", asked, err)
		}

		if len(output.Annotations) != 3 {
			t.Errorf("a page size of %d returned %d marks", asked, len(output.Annotations))
		}
	}
}
