package getannotation_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// written is when the mark below was made.
var written = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *getannotation.GetAnnotation
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
		usecase: getannotation.New(marks, works),
		marks:   marks,
		works:   works,
		reader:  uuid.New(),
		phone:   uuid.New(),
		work:    uuid.New(),
	}

	works.Add(f.work, f.reader)

	return f
}

// mark records one note in the fixture's work and returns it.
func (f *fixture) mark(t *testing.T) *annotation.Annotation {
	t.Helper()

	written := &annotation.Mark{
		Kind:    annotation.KindNote,
		Text:    "uma nota",
		Locator: locator.Locator("page=42"),
	}

	stored, err := annotation.New(f.work, written, f.phone, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = f.marks.Create(t.Context(), stored); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return stored
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.mark(t)

	output, err := f.usecase.Execute(t.Context(),
		getannotation.Input{UserID: f.reader, AnnotationID: stored.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Annotation.ID != stored.ID || output.Annotation.Text != stored.Text {
		t.Errorf("Execute returned %+v", output.Annotation)
	}
}

// Four situations are answered identically, and that is the point: a reply that
// distinguished them would be an oracle for which identifiers exist and whose
// they are.
func TestExecuteAnswersEveryRefusalTheSameWay(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*testing.T, *fixture, *getannotation.Input){
		"no such mark": func(_ *testing.T, _ *fixture, in *getannotation.Input) {
			in.AnnotationID = uuid.New()
		},
		"a mark in another reader's work": func(_ *testing.T, _ *fixture, in *getannotation.Input) {
			in.UserID = uuid.New()
		},
		"a mark that was tombstoned": func(t *testing.T, f *fixture, in *getannotation.Input) {
			t.Helper()

			stored, err := f.marks.GetByID(t.Context(), in.AnnotationID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}

			stored.Delete(f.phone, written)

			if err = f.marks.Update(t.Context(), stored); err != nil {
				t.Fatalf("Update: %v", err)
			}
		},
		"a mark in a work that was tombstoned": func(_ *testing.T, f *fixture, _ *getannotation.Input) {
			f.works.Remove(f.work)
		},
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			stored := f.mark(t)
			input := getannotation.Input{UserID: f.reader, AnnotationID: stored.ID}
			breaks(t, f, &input)

			_, err := f.usecase.Execute(t.Context(), input)
			if !errors.Is(err, errs.KindNotFound) {
				t.Errorf("Execute = %v, want a not found", err)
			}

			if got := errs.CodeOf(err); got != annotation.CodeNotFound {
				t.Errorf("the refusal carries %q, want the mark's own code for all four", got)
			}
		})
	}
}
