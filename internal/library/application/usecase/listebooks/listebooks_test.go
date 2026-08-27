package listebooks_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/listebooks"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
)

// imported is when the first work below entered the collection; the rest
// follow it one microsecond at a time, which is the resolution the column has
// and therefore the interval a tie-break has to survive.
var imported = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// digest is a well-formed content hash.
const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// fill stores count works for reader, one microsecond apart, and returns them
// in the order a page should.
func fill(t *testing.T, works *apptest.EbookRepository, reader uuid.UUID, count int) []*ebook.Ebook {
	t.Helper()

	stored := make([]*ebook.Ebook, 0, count)

	for index := range count {
		at := imported.Add(time.Duration(index) * time.Microsecond)

		work, err := ebook.New(reader,
			&ebook.Details{Title: ebook.Title("work " + string(rune('a'+index)))},
			&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024},
			uuid.New(), at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err := works.Create(t.Context(), work); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Most recently imported first, so the newest goes to the front.
		stored = append([]*ebook.Ebook{work}, stored...)
	}

	return stored
}

func TestExecuteReadsMostRecentlyImportedFirst(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader := uuid.New()
	expected := fill(t, works, reader, 5)

	output, err := listebooks.New(works).Execute(t.Context(), listebooks.Input{UserID: reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Ebooks) != 5 {
		t.Fatalf("the page holds %d works, want 5", len(output.Ebooks))
	}

	for index, work := range output.Ebooks {
		if work.ID != expected[index].ID {
			t.Errorf("position %d is %q, want %q", index, work.Title, expected[index].Title)
		}
	}

	if !output.NextCursor.IsZero() {
		t.Error("a page that held the whole collection reported another one after it")
	}
}

// Walking the cursor has to find every work exactly once, which is the whole
// property keyset pagination is chosen for.
func TestExecuteWalksTheWholeCollectionByCursor(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader := uuid.New()
	expected := fill(t, works, reader, 7)

	usecase := listebooks.New(works)
	seen := make([]uuid.UUID, 0, len(expected))

	var cursor ebook.Cursor

	for range len(expected) {
		output, err := usecase.Execute(t.Context(),
			listebooks.Input{UserID: reader, PageSize: 3, Cursor: cursor})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}

		for _, work := range output.Ebooks {
			seen = append(seen, work.ID)
		}

		cursor = output.NextCursor
		if cursor.IsZero() {
			break
		}
	}

	if len(seen) != len(expected) {
		t.Fatalf("walking the cursor saw %d works, want %d", len(seen), len(expected))
	}

	for index, id := range seen {
		if id != expected[index].ID {
			t.Errorf("position %d is a different work than a single page reported", index)
		}
	}
}

func TestExecuteChoosesAPageSizeForAClientWithNoOpinion(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader := uuid.New()
	fill(t, works, reader, 3)

	output, err := listebooks.New(works).Execute(t.Context(),
		listebooks.Input{UserID: reader, PageSize: 0})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Ebooks) != 3 {
		t.Errorf("a page with no size asked for held %d works", len(output.Ebooks))
	}
}

// A client is not wrong to want the whole collection at once, only wrong about
// what one reply can carry — and the cursor is how it gets the rest, which an
// error here would take away.
func TestExecuteServesTooLargeAPageAtTheCeiling(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader := uuid.New()
	fill(t, works, reader, 2)

	output, err := listebooks.New(works).Execute(t.Context(),
		listebooks.Input{UserID: reader, PageSize: ebook.MaxPageSize + 1000})
	if err != nil {
		t.Fatalf("a page larger than the ceiling was refused: %v", err)
	}

	if len(output.Ebooks) != 2 {
		t.Errorf("the page holds %d works", len(output.Ebooks))
	}
}

func TestExecuteNarrowsToAGrouping(t *testing.T) {
	t.Parallel()

	filings := apptest.NewMembershipRepository()
	works := apptest.NewEbookRepository(filings)
	reader, grouping, phone := uuid.New(), uuid.New(), uuid.New()
	stored := fill(t, works, reader, 3)

	filing, err := membership.New(stored[1].ID, grouping, phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = filings.Create(t.Context(), filing); err != nil {
		t.Fatalf("Create: %v", err)
	}

	output, err := listebooks.New(works).Execute(t.Context(),
		listebooks.Input{UserID: reader, CollectionID: grouping})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Ebooks) != 1 || output.Ebooks[0].ID != stored[1].ID {
		t.Errorf("the shelf holds %d works, want the one filed under it", len(output.Ebooks))
	}
}

// The page is scoped to the reader's works, so a grouping belonging to
// somebody else narrows it to nothing rather than to their shelf. Refusing it
// instead would tell the caller which identifiers exist.
func TestExecuteAnswersAGroupingThatIsNotTheirsWithNothing(t *testing.T) {
	t.Parallel()

	filings := apptest.NewMembershipRepository()
	works := apptest.NewEbookRepository(filings)
	reader := uuid.New()
	fill(t, works, reader, 2)

	output, err := listebooks.New(works).Execute(t.Context(),
		listebooks.Input{UserID: reader, CollectionID: uuid.New()})
	if err != nil {
		t.Fatalf("Execute = %v, want an empty page rather than a refusal", err)
	}

	if len(output.Ebooks) != 0 {
		t.Errorf("the page holds %d works, want none", len(output.Ebooks))
	}
}

func TestExecuteNeverListsATombstone(t *testing.T) {
	t.Parallel()

	works := apptest.NewEbookRepository(nil)
	reader := uuid.New()
	stored := fill(t, works, reader, 3)

	stored[0].Delete(uuid.New(), imported.Add(time.Hour))

	if err := works.Update(t.Context(), stored[0]); err != nil {
		t.Fatalf("Update: %v", err)
	}

	output, err := listebooks.New(works).Execute(t.Context(), listebooks.Input{UserID: reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Ebooks) != 2 {
		t.Errorf("the page holds %d works, want the two that are not tombstones", len(output.Ebooks))
	}
}
