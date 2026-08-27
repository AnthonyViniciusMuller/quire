package createcollection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/createcollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// created is when the groupings below were defined.
var created = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func TestExecute(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)
	reader, phone := uuid.New(), uuid.New()

	output, err := createcollection.New(collections, apptest.NewClock(created)).
		Execute(t.Context(), createcollection.Input{
			UserID:      reader,
			DeviceID:    phone,
			Name:        "Literatura brasileira",
			Kind:        "category",
			Description: "o que a faculdade pediu",
		})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	grouping := output.Collection

	switch {
	case !grouping.BelongsTo(reader):
		t.Error("the grouping does not name the reader who defined it")
	case grouping.Kind != collection.KindCategory:
		t.Errorf("the grouping is a %q", grouping.Kind)
	case grouping.Revision.VectorClock.Get(crdt.Author(phone)) != 1:
		t.Error("the causal history does not count the definition as an event of the device")
	case !grouping.CreatedAt.Equal(created):
		t.Errorf("the grouping was defined at %s, want the clock's %s", grouping.CreatedAt, created)
	}

	if _, err := collections.GetByID(t.Context(), grouping.ID); err != nil {
		t.Fatalf("the grouping was reported and not written: %v", err)
	}
}

// A client that says nothing is making a shelf, which is the default the
// column carries.
func TestExecuteDefaultsToAShelf(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)

	output, err := createcollection.New(collections, apptest.NewClock(created)).
		Execute(t.Context(), createcollection.Input{
			UserID: uuid.New(), DeviceID: uuid.New(), Name: "later",
		})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Collection.Kind != collection.KindCollection {
		t.Errorf("an unstated kind became %q", output.Collection.Kind)
	}
}

// A reader may well have two shelves called "later", and two offline devices
// could not obey a uniqueness rule anyway — neither can see the other's
// shelves until they meet.
func TestExecuteDoesNotMakeTheNameUnique(t *testing.T) {
	t.Parallel()

	collections := apptest.NewCollectionRepository(nil)
	usecase := createcollection.New(collections, apptest.NewClock(created))
	reader, phone := uuid.New(), uuid.New()

	input := createcollection.Input{UserID: reader, DeviceID: phone, Name: "later"}

	first, err := usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	second, err := usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("a second shelf with the same name was refused: %v", err)
	}

	if first.Collection.ID == second.Collection.ID {
		t.Error("the second shelf reused the first rather than being another")
	}
}

func TestExecuteRefusesWhatCannotBeAGrouping(t *testing.T) {
	t.Parallel()

	tests := map[string]createcollection.Input{
		"no name":      {UserID: uuid.New(), DeviceID: uuid.New(), Name: "  "},
		"unknown kind": {UserID: uuid.New(), DeviceID: uuid.New(), Name: "later", Kind: "shelf"},
		"no device":    {UserID: uuid.New(), Name: "later"},
		"no reader":    {DeviceID: uuid.New(), Name: "later"},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			usecase := createcollection.New(apptest.NewCollectionRepository(nil), apptest.NewClock(created))

			if _, err := usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Execute = %v, want an invalid argument", err)
			}
		})
	}
}
