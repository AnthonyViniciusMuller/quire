package updateebook_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/updateebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// imported is when the work below entered the collection, and digest is a
// well-formed content hash.
var imported = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase *updateebook.UpdateEbook
	works   *apptest.EbookRepository
	reader  uuid.UUID
	phone   uuid.UUID
	tablet  uuid.UUID
	work    *ebook.Ebook
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	works := apptest.NewEbookRepository(nil)
	reader, phone, tablet := uuid.New(), uuid.New(), uuid.New()

	work, err := ebook.New(reader,
		&ebook.Details{Title: "Os Sertões", Author: "E. da Cunha", Publisher: "Laemmert"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024},
		phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase: updateebook.New(works, apptest.NewClock(imported.Add(time.Hour))),
		works:   works,
		reader:  reader,
		phone:   phone,
		tablet:  tablet,
		work:    work,
	}
}

// text returns a pointer to s, which is how a field says it is claimed.
func text(s string) *string { return &s }

func TestExecuteWritesOnlyTheFieldsTheMaskNamed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), updateebook.Input{
		UserID:   f.reader,
		DeviceID: f.tablet,
		EbookID:  f.work.ID,
		Changes:  updateebook.Changes{Author: text("Euclides da Cunha")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Ebook.Author != "Euclides da Cunha":
		t.Error("the claimed field was not written")
	case output.Ebook.Title != "Os Sertões":
		t.Error("a field the mask did not name was overwritten, so this write would beat an " +
			"edit from a device it never saw")
	case output.Ebook.Publisher != "Laemmert":
		t.Error("a field the mask did not name was overwritten")
	case output.Ebook.Revision.DeviceID != f.tablet:
		t.Error("the revision names the device that imported the work rather than the one that edited it")
	case !f.work.Revision.VectorClock.HappensBefore(output.Ebook.Revision.VectorClock):
		t.Error("the edit does not causally dominate the version it derives from")
	}
}

// A work whose author is unknown and one whose author the reader deleted are
// the same state, and the reader is entitled to reach it.
func TestExecuteClearsAFieldClaimedAsEmpty(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), updateebook.Input{
		UserID:   f.reader,
		DeviceID: f.phone,
		EbookID:  f.work.ID,
		Changes:  updateebook.Changes{Publisher: text("")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !output.Ebook.Publisher.IsZero() {
		t.Errorf("the publisher is still %q", output.Ebook.Publisher)
	}
}

// It would not be a no-op: it would stamp a revision, and a version claiming a
// write nobody made would win a tie-break against a real edit from a device
// that had been offline.
func TestExecuteRefusesAnEditThatClaimsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), updateebook.Input{
		UserID:   f.reader,
		DeviceID: f.phone,
		EbookID:  f.work.ID,
	})

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Fatalf("Execute = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != updateebook.CodeEmptyUpdate {
		t.Errorf("the refusal is coded %q", code)
	}

	stored, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if !stored.Revision.VectorClock.Equal(f.work.Revision.VectorClock) {
		t.Error("the refused edit stamped a revision anyway")
	}
}

func TestExecuteRefusesAWorkTheReaderCannotSee(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	tests := map[string]updateebook.Input{
		"no such work": {
			UserID: f.reader, DeviceID: f.phone, EbookID: uuid.New(),
			Changes: updateebook.Changes{Title: text("anything")},
		},
		"somebody else's": {
			UserID: uuid.New(), DeviceID: f.phone, EbookID: f.work.ID,
			Changes: updateebook.Changes{Title: text("anything")},
		},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindNotFound) {
				t.Errorf("Execute = %v, want a not found", err)
			}
		})
	}
}

// The file is fixed at import: the input has no field for it, and the write
// must leave it alone even so.
func TestExecuteLeavesTheFileAlone(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), updateebook.Input{
		UserID:   f.reader,
		DeviceID: f.phone,
		EbookID:  f.work.ID,
		Changes:  updateebook.Changes{Title: text("Os Sertões (1902)")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Ebook.Hash != digest || output.Ebook.Format != ebook.FormatEPUB || output.Ebook.Size != 1024 {
		t.Error("editing the description changed what the bytes are")
	}
}

func TestExecuteValidatesWhatItIsAskedToWrite(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), updateebook.Input{
		UserID:   f.reader,
		DeviceID: f.phone,
		EbookID:  f.work.ID,
		Changes:  updateebook.Changes{Title: text("   ")},
	})

	if errs.CodeOf(err) != ebook.CodeInvalidTitle {
		t.Errorf("an edit that would leave the work untitled was refused as %q", errs.CodeOf(err))
	}
}
