package getcollection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// created is when the groupings below were defined.
var created = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// grouping returns a shelf belonging to reader, stored in collections.
func grouping(t *testing.T, collections *apptest.CollectionRepository, reader uuid.UUID) *collection.Collection {
	t.Helper()

	stored, err := collection.New(reader,
		&collection.Details{Name: "Literatura brasileira", Kind: collection.KindCollection},
		uuid.New(), created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := collections.Create(t.Context(), stored); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return stored
}

func TestExecute(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)
	reader := uuid.New()
	stored := grouping(t, collections, reader)

	output, err := getcollection.New(collections).Execute(t.Context(),
		getcollection.Input{UserID: reader, CollectionID: stored.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Collection.ID != stored.ID {
		t.Error("the reply is a different grouping")
	}
}

// A reply that told the three apart would be an oracle for which identifiers
// exist and whose they are.
func TestExecuteAnswersTheThreeInvisibilitiesIdentically(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)
	reader, stranger := uuid.New(), uuid.New()

	removed := grouping(t, collections, reader)
	removed.Delete(uuid.New(), created.Add(time.Hour))

	if err := collections.Update(t.Context(), removed); err != nil {
		t.Fatalf("Update: %v", err)
	}

	theirs := grouping(t, collections, stranger)

	tests := map[string]getcollection.Input{
		"no such grouping": {UserID: reader, CollectionID: uuid.New()},
		"somebody else's":  {UserID: reader, CollectionID: theirs.ID},
		"one they deleted": {UserID: reader, CollectionID: removed.ID},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := getcollection.New(collections).Execute(t.Context(), input)

			if !errors.Is(err, errs.KindNotFound) {
				t.Fatalf("Execute = %v, want a not found", err)
			}

			if code := errs.CodeOf(err); code != collection.CodeNotFound {
				t.Errorf("the refusal is coded %q, want the same %q every other one carries",
					code, collection.CodeNotFound)
			}
		})
	}
}
