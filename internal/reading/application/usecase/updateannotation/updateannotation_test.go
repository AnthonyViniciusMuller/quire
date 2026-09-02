package updateannotation_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// made is when the mark was written, and edited is when the clock stands for
// the edit.
var (
	made   = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	edited = made.Add(time.Hour)
)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *updateannotation.UpdateAnnotation
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
		usecase: updateannotation.New(marks, works, apptest.NewClock(edited)),
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

// text returns a pointer to s, which is how a claimed field is spelled.
func text(s string) *string { return &s }

// C10: after an edit from a second device, the device the row names and the
// device that made the mark are different, and the tie-break needs the first.
func TestExecuteNamesTheDeviceWhoseWriteTheRowReflects(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)

	output, err := f.usecase.Execute(t.Context(), updateannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.tablet,
		AnnotationID: stored.ID,
		Changes:      updateannotation.Changes{Text: text("uma nota corrigida")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	mark := output.Annotation

	switch {
	case mark.Text.String() != "uma nota corrigida":
		t.Errorf("the mark reads %q", mark.Text)
	case mark.Revision.DeviceID != f.tablet:
		t.Error("the revision still names the device that made the mark")
	case mark.Revision.VectorClock.Get(crdt.Author(f.phone)) != 1:
		t.Error("the edit dropped the creating device from the causal history")
	case mark.Revision.VectorClock.Get(crdt.Author(f.tablet)) != 1:
		t.Error("the causal history does not count the edit as an event of the editing device")
	case !mark.Revision.UpdatedAt.Equal(edited):
		t.Errorf("the edit was stamped %s, want the clock's %s", mark.Revision.UpdatedAt, edited)
	}
}

// A field the mask did not name is left to whichever device wrote it last,
// which on a per-field last-writer-wins entity is the difference between
// winning and losing a tie-break against a device this one has never seen.
func TestExecuteWritesOnlyWhatTheMaskClaimed(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)

	output, err := f.usecase.Execute(t.Context(), updateannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
		Changes:      updateannotation.Changes{Locator: text("page=99")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Annotation.Text != stored.Text || output.Annotation.Kind != stored.Kind {
		t.Errorf("an unclaimed field was written: %+v", output.Annotation.Mark)
	}

	if output.Annotation.Locator.String() != "page=99" {
		t.Errorf("the passage is %q", output.Annotation.Locator)
	}
}

// An edit that claims nothing would still stamp a revision, and a version that
// claims a write nobody made would win a tie-break against a real edit from a
// device that had been offline.
func TestExecuteRefusesAnEditThatClaimsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)

	_, err := f.usecase.Execute(t.Context(), updateannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
	})
	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Execute = %v, want an invalid argument", err)
	}

	if errs.CodeOf(err) != updateannotation.CodeEmptyUpdate {
		t.Errorf("the refusal carries %q", errs.CodeOf(err))
	}
}

// The note rule is checked against the result and not against the claim: a mask
// naming only the text, on a row whose kind is a note, has to be read together
// with that kind.
func TestExecuteRefusesEmptyingTheTextOfANote(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)

	_, err := f.usecase.Execute(t.Context(), updateannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
		Changes:      updateannotation.Changes{Text: text("   ")},
	})
	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Execute = %v, want an invalid argument", err)
	}

	// The same mask with the kind alongside it is a reader turning their note
	// into a highlight, which is what they are actually asking for.
	kind := "highlight"

	output, err := f.usecase.Execute(t.Context(), updateannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
		Changes:      updateannotation.Changes{Kind: &kind, Text: text("")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Annotation.Kind != annotation.KindHighlight || !output.Annotation.Text.IsZero() {
		t.Errorf("the mark is %+v", output.Annotation.Mark)
	}
}

func TestExecuteRefusesAMarkTheReaderMayNotSee(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*testing.T, *fixture, *updateannotation.Input){
		"no such mark": func(_ *testing.T, _ *fixture, in *updateannotation.Input) {
			in.AnnotationID = uuid.New()
		},
		"a mark in another reader's work": func(_ *testing.T, _ *fixture, in *updateannotation.Input) {
			in.UserID = uuid.New()
		},
		"a mark that was tombstoned": func(t *testing.T, f *fixture, in *updateannotation.Input) {
			t.Helper()

			stored, err := f.marks.GetByID(t.Context(), in.AnnotationID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}

			stored.Delete(f.phone, made)

			if err = f.marks.Update(t.Context(), stored); err != nil {
				t.Fatalf("Update: %v", err)
			}
		},
		"a mark in a work that was tombstoned": func(_ *testing.T, f *fixture, _ *updateannotation.Input) {
			f.works.Remove(f.work)
		},
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			stored := f.note(t)
			input := updateannotation.Input{
				UserID:       f.reader,
				DeviceID:     f.phone,
				AnnotationID: stored.ID,
				Changes:      updateannotation.Changes{Text: text("corrigida")},
			}
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

// A library that could not be read is not a mark that does not exist: a
// client told the mark is gone would believe it and stop asking.
func TestExecuteReportsALibraryThatCouldNotBeRead(t *testing.T) {
	t.Parallel()

	f := newFixture()
	stored := f.note(t)
	f.works.Err = errs.New(errs.KindUnavailable, "the database is unavailable")

	_, err := f.usecase.Execute(t.Context(), updateannotation.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		AnnotationID: stored.ID,
		Changes:      updateannotation.Changes{Text: text("outra nota")},
	})
	if !errors.Is(err, errs.KindUnavailable) {
		t.Errorf("Execute = %v, want the library's own failure", err)
	}
}
