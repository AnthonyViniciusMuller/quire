package deletecollection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/deletecollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// created is when the grouping below was defined.
var created = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase     *deletecollection.DeleteCollection
	collections *apptest.CollectionRepository
	filings     *apptest.MembershipRepository
	transaction *apptest.Transaction
	reader      uuid.UUID
	phone       uuid.UUID
	grouping    *collection.Collection
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	filings := apptest.NewMembershipRepository()
	collections := apptest.NewCollectionRepository(filings)
	transaction := apptest.NewTransaction()
	reader, phone := uuid.New(), uuid.New()

	grouping, err := collection.New(reader,
		&collection.Details{Name: "Literatura", Kind: collection.KindCollection}, phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := collections.Create(t.Context(), grouping); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase: deletecollection.New(collections, filings,
			apptest.NewClock(created.Add(time.Hour)), transaction),
		collections: collections,
		filings:     filings,
		transaction: transaction,
		reader:      reader,
		phone:       phone,
		grouping:    grouping,
	}
}

// file puts a work on the shelf, so that the deletion has something to clear.
func (f *fixture) file(t *testing.T) uuid.UUID {
	t.Helper()

	work := uuid.New()

	filing, err := membership.New(work, f.grouping.ID, f.phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.filings.Create(t.Context(), filing); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return work
}

func TestExecuteTombstonesTheGroupingAndClearsItsFilings(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.file(t)
	f.file(t)

	if _, err := f.usecase.Execute(t.Context(), deletecollection.Input{
		UserID: f.reader, DeviceID: f.phone, CollectionID: f.grouping.ID,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := f.collections.GetByID(t.Context(), f.grouping.ID)
	if err != nil {
		t.Fatalf("the row went, so a node that had not heard about the deletion "+
			"would resurrect it: %v", err)
	}

	switch {
	case !stored.IsDeleted():
		t.Error("the grouping was not marked removed")
	case len(f.filings.Filed()) != 0:
		t.Errorf("%d works are still on the shelf", len(f.filings.Filed()))
	case f.transaction.Calls() != 1:
		t.Errorf("the writes were made in %d units of work, want one", f.transaction.Calls())
	}
}

// Both calls read the grouping and then write a row that references it, so
// without the lock a filing could be written against a grouping tombstoned in
// between — and no later deletion would clear it.
func TestExecuteTakesTheRowLockFilingAWorkContendsFor(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), deletecollection.Input{
		UserID: f.reader, DeviceID: f.phone, CollectionID: f.grouping.ID,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	locked := f.collections.Locked()
	if len(locked) != 1 || locked[0] != f.grouping.ID {
		t.Errorf("the deletion locked %v, want the grouping it was about", locked)
	}
}

func TestExecuteAnswersASecondDeletionAsAGroupingThatIsNotThere(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	input := deletecollection.Input{UserID: f.reader, DeviceID: f.phone, CollectionID: f.grouping.ID}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	first, err := f.collections.GetByID(t.Context(), f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if _, err = f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindNotFound) {
		t.Fatalf("Execute = %v, want a not found", err)
	}

	second, err := f.collections.GetByID(t.Context(), f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if !second.Revision.VectorClock.Equal(first.Revision.VectorClock) {
		t.Error("the refused deletion stamped a second revision anyway")
	}
}

func TestExecuteRefusesAGroupingThatIsNotTheirs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.file(t)

	_, err := f.usecase.Execute(t.Context(), deletecollection.Input{
		UserID: uuid.New(), DeviceID: f.phone, CollectionID: f.grouping.ID,
	})

	if !errors.Is(err, errs.KindNotFound) {
		t.Fatalf("Execute = %v, want a not found", err)
	}

	if len(f.filings.Filed()) != 1 {
		t.Error("a stranger's deletion cleared the shelf")
	}
}
