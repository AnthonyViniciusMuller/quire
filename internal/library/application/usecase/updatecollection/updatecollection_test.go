package updatecollection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/updatecollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// created is when the grouping below was defined.
var created = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase     *updatecollection.UpdateCollection
	collections *apptest.CollectionRepository
	reader      uuid.UUID
	phone       uuid.UUID
	tablet      uuid.UUID
	grouping    *collection.Collection
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	collections := apptest.NewCollectionRepository(nil)
	reader, phone, tablet := uuid.New(), uuid.New(), uuid.New()

	grouping, err := collection.New(reader, &collection.Details{
		Name: "Literatura", Kind: collection.KindCollection, Description: "o que sobrou",
	}, phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := collections.Create(t.Context(), grouping); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase:     updatecollection.New(collections, apptest.NewClock(created.Add(time.Hour))),
		collections: collections,
		reader:      reader,
		phone:       phone,
		tablet:      tablet,
		grouping:    grouping,
	}
}

// text returns a pointer to s, which is how a field says it is claimed.
func text(s string) *string { return &s }

func TestExecuteWritesOnlyTheFieldsTheMaskNamed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), updatecollection.Input{
		UserID:       f.reader,
		DeviceID:     f.tablet,
		CollectionID: f.grouping.ID,
		Changes:      updatecollection.Changes{Name: text("Literatura brasileira")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Collection.Name != "Literatura brasileira":
		t.Error("the claimed field was not written")
	case output.Collection.Description != "o que sobrou":
		t.Error("a field the mask did not name was overwritten")
	case output.Collection.Revision.DeviceID != f.tablet:
		t.Error("the revision names the device that defined the grouping rather than the one that edited it")
	case !f.grouping.Revision.VectorClock.HappensBefore(output.Collection.Revision.VectorClock):
		t.Error("the edit does not causally dominate the version it derives from")
	}
}

func TestExecuteChangesWhatAGroupingMeans(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), updatecollection.Input{
		UserID:       f.reader,
		DeviceID:     f.phone,
		CollectionID: f.grouping.ID,
		Changes:      updatecollection.Changes{Kind: text("category")},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Collection.Kind != collection.KindCategory {
		t.Errorf("the grouping is a %q", output.Collection.Kind)
	}
}

func TestExecuteRefusesAnEditThatClaimsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), updatecollection.Input{
		UserID: f.reader, DeviceID: f.phone, CollectionID: f.grouping.ID,
	})

	if errs.CodeOf(err) != updatecollection.CodeEmptyUpdate {
		t.Errorf("Execute = %v, want an edit that claims nothing to be refused", err)
	}

	stored, err := f.collections.GetByID(t.Context(), f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if !stored.Revision.VectorClock.Equal(f.grouping.Revision.VectorClock) {
		t.Error("the refused edit stamped a revision anyway")
	}
}

func TestExecuteRefusesAGroupingTheReaderCannotSee(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	tests := map[string]updatecollection.Input{
		"no such grouping": {
			UserID: f.reader, DeviceID: f.phone, CollectionID: uuid.New(),
			Changes: updatecollection.Changes{Name: text("anything")},
		},
		"somebody else's": {
			UserID: uuid.New(), DeviceID: f.phone, CollectionID: f.grouping.ID,
			Changes: updatecollection.Changes{Name: text("anything")},
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

func TestExecuteValidatesWhatItIsAskedToWrite(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), updatecollection.Input{
		UserID: f.reader, DeviceID: f.phone, CollectionID: f.grouping.ID,
		Changes: updatecollection.Changes{Kind: text("shelf")},
	})

	if errs.CodeOf(err) != collection.CodeInvalidKind {
		t.Errorf("an unknown kind was refused as %q", errs.CodeOf(err))
	}
}
