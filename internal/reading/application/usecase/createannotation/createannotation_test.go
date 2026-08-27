package createannotation_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/createannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// written is when the marks below were made.
var written = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *createannotation.CreateAnnotation
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
		usecase: createannotation.New(marks, works, apptest.NewClock(written)),
		marks:   marks,
		works:   works,
		reader:  uuid.New(),
		phone:   uuid.New(),
		work:    uuid.New(),
	}

	works.Add(f.work, f.reader)

	return f
}

// input is a well-formed note.
func (f *fixture) input() createannotation.Input {
	return createannotation.Input{
		UserID:   f.reader,
		DeviceID: f.phone,
		EbookID:  f.work,
		Kind:     "note",
		Text:     "a sertão é uma sociedade",
		Locator:  "epubcfi(/6/14!/4/10/3:10)",
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	mark := output.Annotation

	switch {
	case mark.ID == (uuid.UUID{}):
		t.Error("the mark was recorded without an identifier the reply could carry")
	case !mark.IsIn(f.work):
		t.Error("the mark does not name the work it was made in")
	case mark.Revision.DeviceID != f.phone:
		t.Error("the revision does not name the device that made the mark")
	case mark.Revision.VectorClock.Get(crdt.Author(f.phone)) != 1:
		t.Error("the causal history does not count the mark as an event of the writing device")
	case !mark.Revision.UpdatedAt.Equal(written):
		t.Errorf("the mark was stamped %s, want the clock's %s", mark.Revision.UpdatedAt, written)
	}

	if _, err = f.marks.GetByID(t.Context(), mark.ID); err != nil {
		t.Fatalf("the mark was reported and not written: %v", err)
	}
}

// The work is what establishes whose the mark is, so a reader writing in a book
// that is not theirs is told there is no such book — not that their note is
// malformed, and not that the book belongs to somebody else.
func TestExecuteRefusesAWorkTheReaderMayNotSee(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*fixture, *createannotation.Input){
		"a work that does not exist": func(_ *fixture, in *createannotation.Input) {
			in.EbookID = uuid.New()
		},
		"a work that was tombstoned": func(f *fixture, _ *createannotation.Input) {
			f.works.Remove(f.work)
		},
		"a work belonging to somebody else": func(_ *fixture, in *createannotation.Input) {
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

// The work is checked before the mark is parsed, so that a reader writing in
// somebody else's book learns nothing about their own request.
func TestExecuteChecksTheWorkBeforeItParsesAnything(t *testing.T) {
	t.Parallel()

	f := newFixture()
	input := f.input()
	input.EbookID = uuid.New()
	input.Kind = "underline"

	if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("Execute = %v, want the work's refusal rather than the field's", err)
	}
}

func TestExecuteRefusesWhatCannotBeAMark(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*createannotation.Input){
		"an unknown kind":       func(in *createannotation.Input) { in.Kind = "underline" },
		"no kind":               func(in *createannotation.Input) { in.Kind = "" },
		"no passage":            func(in *createannotation.Input) { in.Locator = "  " },
		"a note saying nothing": func(in *createannotation.Input) { in.Text = "   " },
		"no device":             func(in *createannotation.Input) { in.DeviceID = uuid.UUID{} },
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

// A highlight is about the passage and carries text only if the reader gave it
// one, which is the case the note rule must not catch.
func TestExecuteAcceptsAHighlightWithNothingWrittenOnIt(t *testing.T) {
	t.Parallel()

	f := newFixture()
	input := f.input()
	input.Kind = "highlight"
	input.Text = ""

	output, err := f.usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Annotation.Kind != annotation.KindHighlight || !output.Annotation.Text.IsZero() {
		t.Errorf("the mark was recorded as %+v", output.Annotation.Mark)
	}
}
