package listcollections_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/listcollections"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
)

// created is when the groupings below were defined.
var created = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// define stores a grouping called name, belonging to reader.
func define(
	t *testing.T, collections *apptest.CollectionRepository, reader uuid.UUID, name string,
) *collection.Collection {
	t.Helper()

	stored, err := collection.New(reader,
		&collection.Details{Name: collection.Name(name), Kind: collection.KindCollection},
		uuid.New(), created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := collections.Create(t.Context(), stored); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return stored
}

// A list that reshuffled between two calls would make a client redraw a shelf
// list that did not change.
func TestExecuteOrdersByName(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)
	reader := uuid.New()

	define(t, collections, reader, "poesia")
	define(t, collections, reader, "ensaios")
	define(t, collections, reader, "romances")

	output, err := listcollections.New(collections).Execute(t.Context(),
		listcollections.Input{UserID: reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := []string{"ensaios", "poesia", "romances"}
	if len(output.Collections) != len(want) {
		t.Fatalf("the reply holds %d groupings, want %d", len(output.Collections), len(want))
	}

	for index, grouping := range output.Collections {
		if grouping.Name.String() != want[index] {
			t.Errorf("position %d is %q, want %q", index, grouping.Name, want[index])
		}
	}
}

func TestExecuteNarrowsToTheGroupingsOneWorkIsFiledUnder(t *testing.T) {
	t.Parallel()

	filings := apptest.NewMembershipRepository()
	collections := apptest.NewCollectionRepository(filings)
	reader, work, phone := uuid.New(), uuid.New(), uuid.New()

	shelved := define(t, collections, reader, "ensaios")
	define(t, collections, reader, "poesia")

	filing, err := membership.New(work, shelved.ID, phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = filings.Create(t.Context(), filing); err != nil {
		t.Fatalf("Create: %v", err)
	}

	output, err := listcollections.New(collections).Execute(t.Context(),
		listcollections.Input{UserID: reader, EbookID: work})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Collections) != 1 || output.Collections[0].ID != shelved.ID {
		t.Errorf("the reply holds %d groupings, want the one the work is on", len(output.Collections))
	}
}

func TestExecuteNeverListsATombstoneOrSomebodyElsesShelf(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)
	reader := uuid.New()

	define(t, collections, reader, "ensaios")
	define(t, collections, uuid.New(), "somebody else's")

	removed := define(t, collections, reader, "poesia")
	removed.Delete(uuid.New(), created.Add(time.Hour))

	if err := collections.Update(t.Context(), removed); err != nil {
		t.Fatalf("Update: %v", err)
	}

	output, err := listcollections.New(collections).Execute(t.Context(),
		listcollections.Input{UserID: reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Collections) != 1 || output.Collections[0].Name != "ensaios" {
		t.Errorf("the reply holds %d groupings, want only the reader's own that is not a tombstone",
			len(output.Collections))
	}
}
