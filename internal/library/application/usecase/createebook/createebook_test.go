package createebook_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/createebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// digest is a well-formed content hash, and imported is when the works below
// entered the collection.
const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

var imported = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase  *createebook.CreateEbook
	works    *apptest.EbookRepository
	contents *apptest.ContentRepository
	reader   uuid.UUID
	phone    uuid.UUID
}

func newFixture() *fixture {
	works := apptest.NewEbookRepository(nil)
	contents := apptest.NewContentRepository()

	return &fixture{
		usecase:  createebook.New(works, contents, apptest.NewClock(imported)),
		works:    works,
		contents: contents,
		reader:   uuid.New(),
		phone:    uuid.New(),
	}
}

// input is a well-formed import.
func (f *fixture) input() createebook.Input {
	return createebook.Input{
		UserID:      f.reader,
		DeviceID:    f.phone,
		Title:       "Os Sertões",
		Author:      "Euclides da Cunha",
		Format:      "epub",
		ContentHash: digest,
		Size:        1024,
		Extra:       map[string]any{"isbn": "9788535911190"},
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	work := output.Ebook

	switch {
	case work.ID == (uuid.UUID{}):
		t.Error("the work was recorded without an identifier the reply could carry")
	case !work.BelongsTo(f.reader):
		t.Error("the work does not name the reader whose collection it joined")
	case work.Revision.DeviceID != f.phone:
		t.Error("the revision does not name the device that imported the work")
	case work.Revision.VectorClock.Get(crdt.Author(f.phone)) != 1:
		t.Error("the causal history does not count the import as an event of the importing device")
	case !work.ImportedAt.Equal(imported):
		t.Errorf("the work entered the collection at %s, want the clock's %s", work.ImportedAt, imported)
	}

	stored, err := f.works.GetByID(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("the work was reported and not written: %v", err)
	}

	if stored.Title != "Os Sertões" {
		t.Errorf("the stored work is titled %q", stored.Title)
	}
}

// It is the client's only cue to upload, and it is false far more often than a
// client might expect: the object is keyed by its digest, so the same file
// imported on a second device or by a second reader is already here.
func TestExecuteReportsWhetherThisNodeHoldsTheBytes(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !output.ContentMissing {
		t.Error("a node that holds nothing reported that it already had the file")
	}

	held, err := content.New(digest, 1024, "application/epub+zip",
		content.Locator{Bucket: "quire-test", Key: "ebooks/1a/2b/" + digest}, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = f.contents.Create(t.Context(), held); err != nil {
		t.Fatalf("Create: %v", err)
	}

	second, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if second.ContentMissing {
		t.Error("a second import of a file this node already holds asked for the bytes again")
	}

	if second.Ebook.ID == output.Ebook.ID {
		t.Error("the second import reused the first work rather than recording another")
	}
}

func TestExecuteRefusesWhatCannotBeAWork(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*createebook.Input){
		"no title":       func(in *createebook.Input) { in.Title = "  " },
		"unknown format": func(in *createebook.Input) { in.Format = "txt" },
		"no digest":      func(in *createebook.Input) { in.ContentHash = "" },
		"a digest that is not one": func(in *createebook.Input) {
			in.ContentHash = "sha256:" + digest
		},
		"a negative length": func(in *createebook.Input) { in.Size = -1 },
		"no device":         func(in *createebook.Input) { in.DeviceID = uuid.UUID{} },
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

// The digest is the storage key, so a client that sent it in upper case must
// converge on the same object as one that sent it in lower case.
func TestExecuteLowercasesTheDigest(t *testing.T) {
	t.Parallel()

	f := newFixture()
	input := f.input()
	input.ContentHash = "1A2B3C4D5E6F708192A3B4C5D6E7F8091A2B3C4D5E6F708192A3B4C5D6E7F809"

	output, err := f.usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Ebook.Hash != ebook.ContentHash(digest) {
		t.Errorf("the work names the file as %q", output.Ebook.Hash)
	}
}
