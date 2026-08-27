package addtocollection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/addtocollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// at is when everything below was written, and digest is a well-formed content
// hash.
var at = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase     *addtocollection.AddToCollection
	works       *apptest.EbookRepository
	collections *apptest.CollectionRepository
	filings     *apptest.MembershipRepository
	clock       *apptest.Clock
	reader      uuid.UUID
	phone       uuid.UUID
	work        *ebook.Ebook
	grouping    *collection.Collection
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	filings := apptest.NewMembershipRepository()
	works := apptest.NewEbookRepository(filings)
	collections := apptest.NewCollectionRepository(filings)
	clock := apptest.NewClock(at)
	reader, phone := uuid.New(), uuid.New()

	f := &fixture{
		usecase: addtocollection.New(works, collections, filings, clock, apptest.NewTransaction()),
		works:   works, collections: collections, filings: filings, clock: clock,
		reader: reader, phone: phone,
	}

	f.work = f.storeWork(t, reader)
	f.grouping = f.storeGrouping(t, reader)

	return f
}

func (f *fixture) storeWork(t *testing.T, owner uuid.UUID) *ebook.Ebook {
	t.Helper()

	work, err := ebook.New(owner, &ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024}, f.phone, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return work
}

func (f *fixture) storeGrouping(t *testing.T, owner uuid.UUID) *collection.Collection {
	t.Helper()

	grouping, err := collection.New(owner,
		&collection.Details{Name: "Literatura", Kind: collection.KindCollection}, f.phone, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.collections.Create(t.Context(), grouping); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return grouping
}

// input is the well-formed filing of the fixture's pair.
func (f *fixture) input() addtocollection.Input {
	return addtocollection.Input{
		UserID: f.reader, DeviceID: f.phone,
		EbookID: f.work.ID, CollectionID: f.grouping.ID,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	filing, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	if !filing.IsFiled() {
		t.Error("the work is not on the shelf")
	}

	if filing.Revision.DeviceID != f.phone {
		t.Error("the register does not name the device that set it")
	}
}

// The pair is unique (C06) and the row is a register, so a second call reuses
// the row rather than appending a second one — which is exactly what two
// offline devices will produce.
func TestExecuteIsIdempotentAndStillStampsAWrite(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	first, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	f.clock.Advance(time.Hour)

	if _, err = f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("a second filing of the same pair was refused: %v", err)
	}

	if filed := f.filings.Filed(); len(filed) != 1 {
		t.Fatalf("the work is filed %d times under the same grouping, want one register", len(filed))
	}

	second, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	switch {
	case second.ID != first.ID:
		t.Error("the second filing wrote a second row rather than setting the register")
	case !first.Revision.VectorClock.HappensBefore(second.Revision.VectorClock):
		t.Error("the write was not recorded in the causal history, so an older removal from " +
			"another device would win the tie-break it should have lost")
	}
}

// Filing a work that another device took off the shelf is how a reader undoes
// that removal, and the register is what makes it one write rather than two
// rows.
func TestExecuteRefilesAWorkThatWasTakenOff(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	filing, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	filing.Clear(f.phone, at.Add(time.Minute))

	if err = f.filings.Update(t.Context(), filing); err != nil {
		t.Fatalf("Update: %v", err)
	}

	f.clock.Advance(time.Hour)

	if _, err = f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	refiled, err := f.filings.GetByPair(t.Context(), f.work.ID, f.grouping.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	if !refiled.IsFiled() {
		t.Error("the work was not put back on the shelf")
	}
}

// A filing is a fact about two rows, and a call that could name one of each
// would be a way to learn that the other reader's exists.
func TestExecuteRefusesAPairTheReaderDoesNotOwnBothHalvesOf(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	stranger := uuid.New()
	theirWork := f.storeWork(t, stranger)
	theirGrouping := f.storeGrouping(t, stranger)

	tests := map[string]addtocollection.Input{
		"their work": {
			UserID: f.reader, DeviceID: f.phone,
			EbookID: theirWork.ID, CollectionID: f.grouping.ID,
		},
		"their grouping": {
			UserID: f.reader, DeviceID: f.phone,
			EbookID: f.work.ID, CollectionID: theirGrouping.ID,
		},
		"no such work": {
			UserID: f.reader, DeviceID: f.phone,
			EbookID: uuid.New(), CollectionID: f.grouping.ID,
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

	if filed := f.filings.Filed(); len(filed) != 0 {
		t.Errorf("%d filings were written by refused calls", len(filed))
	}
}

// Both calls read the grouping and then write a row that references it, so the
// filing has to take the lock the deletion takes.
func TestExecuteTakesTheRowLockDeletingTheGroupingContendsFor(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	locked := f.collections.Locked()
	if len(locked) != 1 || locked[0] != f.grouping.ID {
		t.Errorf("the filing locked %v, want the grouping it was about", locked)
	}
}
