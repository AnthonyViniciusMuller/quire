package removefromcollection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/removefromcollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// at is when everything below was written, and digest is a well-formed content
// hash.
var at = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase  *removefromcollection.RemoveFromCollection
	filings  *apptest.MembershipRepository
	reader   uuid.UUID
	phone    uuid.UUID
	work     *ebook.Ebook
	grouping *collection.Collection
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	filings := apptest.NewMembershipRepository()
	works := apptest.NewEbookRepository(filings)
	collections := apptest.NewCollectionRepository(filings)
	reader, phone := uuid.New(), uuid.New()

	work, err := ebook.New(reader, &ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024}, phone, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}

	grouping, err := collection.New(reader,
		&collection.Details{Name: "Literatura", Kind: collection.KindCollection}, phone, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := collections.Create(t.Context(), grouping); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase: removefromcollection.New(works, collections, filings,
			apptest.NewClock(at.Add(time.Hour)), apptest.NewTransaction()),
		filings: filings, reader: reader, phone: phone, work: work, grouping: grouping,
	}
}

// file puts the work on the shelf.
func (f *fixture) file(t *testing.T) *membership.Membership {
	t.Helper()

	filing, err := membership.New(f.work.ID, f.grouping.ID, f.phone, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.filings.Create(t.Context(), filing); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return filing
}

// input is the well-formed removal of the fixture's pair.
func (f *fixture) input() removefromcollection.Input {
	return removefromcollection.Input{
		UserID: f.reader, DeviceID: f.phone,
		EbookID: f.work.ID, CollectionID: f.grouping.ID,
	}
}

func TestExecuteClearsTheRegisterRatherThanRemovingTheRow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	filed := f.file(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	filing, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID)
	if err != nil {
		t.Fatalf("the row went, so a node that had not heard about the removal "+
			"would put the work back on the shelf: %v", err)
	}

	switch {
	case filing.IsFiled():
		t.Error("the work is still on the shelf")
	case filing.Revision.DeviceID != f.phone:
		t.Error("the tombstone does not name the device that made it")
	case !filed.Revision.VectorClock.HappensBefore(filing.Revision.VectorClock):
		t.Error("the removal does not causally dominate the filing it removed")
	}
}

// The row is a register, so clearing one that was already clear is the state
// the caller asked for.
func TestExecuteIsIdempotent(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.file(t)

	for range 2 {
		if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}

	if filed := f.filings.Filed(); len(filed) != 0 {
		t.Errorf("%d works are still on the shelf", len(filed))
	}
}

// A row created only to be tombstoned would claim that this device once filed
// the work, which is a history that did not happen — and the register being
// absent already means the work is not on the shelf.
func TestExecuteWritesNothingForAPairThatWasNeverFiled(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("removing a work that was never on the shelf was refused: %v", err)
	}

	if _, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID); !errors.Is(err, errs.KindNotFound) {
		t.Error("a row was written for a filing that never happened")
	}
}

func TestExecuteRefusesAPairTheReaderDoesNotOwnBothHalvesOf(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.file(t)

	tests := map[string]removefromcollection.Input{
		"somebody else asking": {
			UserID: uuid.New(), DeviceID: f.phone,
			EbookID: f.work.ID, CollectionID: f.grouping.ID,
		},
		"no such grouping": {
			UserID: f.reader, DeviceID: f.phone,
			EbookID: f.work.ID, CollectionID: uuid.New(),
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

	if filed := f.filings.Filed(); len(filed) != 1 {
		t.Error("a refused removal took the work off the shelf anyway")
	}
}
