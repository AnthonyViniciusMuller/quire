package getebook_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// imported is when the works below entered the collection.
var imported = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// digest is a well-formed content hash.
const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// work returns a work in the reader's collection, stored in works.
func work(t *testing.T, works *apptest.EbookRepository, reader uuid.UUID) *ebook.Ebook {
	t.Helper()

	stored, err := ebook.New(reader,
		&ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024},
		uuid.New(), imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := works.Create(t.Context(), stored); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return stored
}

func TestExecute(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader := uuid.New()
	stored := work(t, works, reader)

	output, err := getebook.New(works).Execute(t.Context(),
		getebook.Input{UserID: reader, EbookID: stored.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Ebook.ID != stored.ID || output.Ebook.Title != "Os Sertões" {
		t.Errorf("the reply is a different work: %+v", output.Ebook)
	}
}

// A reply that told the three apart would be an oracle for which identifiers
// exist and whose they are, and the client can do nothing different with any
// of them.
func TestExecuteAnswersTheThreeInvisibilitiesIdentically(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader, stranger := uuid.New(), uuid.New()

	mine := work(t, works, reader)

	removed := work(t, works, reader)
	removed.Delete(uuid.New(), imported.Add(time.Hour))

	if err := works.Update(t.Context(), removed); err != nil {
		t.Fatalf("Update: %v", err)
	}

	theirs := work(t, works, stranger)

	tests := map[string]getebook.Input{
		"no such work":     {UserID: reader, EbookID: uuid.New()},
		"somebody else's":  {UserID: reader, EbookID: theirs.ID},
		"one they deleted": {UserID: reader, EbookID: removed.ID},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := getebook.New(works).Execute(t.Context(), input)

			if !errors.Is(err, errs.KindNotFound) {
				t.Fatalf("Execute = %v, want a not found", err)
			}

			if code := errs.CodeOf(err); code != ebook.CodeNotFound {
				t.Errorf("the refusal is coded %q, want the same %q every other one carries",
					code, ebook.CodeNotFound)
			}
		})
	}

	// And the one that is visible still is, so the checks above are not
	// refusing everything.
	if _, err := getebook.New(works).Execute(t.Context(),
		getebook.Input{UserID: reader, EbookID: mine.ID}); err != nil {
		t.Errorf("the reader's own work was refused: %v", err)
	}
}
