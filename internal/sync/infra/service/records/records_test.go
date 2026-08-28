package records_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"uuid"

	libraryapptest "github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	readingapptest "github.com/anthonyvsmuller/quire/internal/reading/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/service/records"
)

// authored is when the operations below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is the reconciler over the doubles of the two slices that own the
// replicated records, and the identifiers a test addresses them by.
type fixture struct {
	reconciler *records.Service
	works      *libraryapptest.EbookRepository
	groupings  *libraryapptest.CollectionRepository
	filings    *libraryapptest.MembershipRepository
	marks      *readingapptest.AnnotationRepository
	positions  *readingapptest.ProgressRepository

	reader uuid.UUID
	phone  uuid.UUID
	tablet uuid.UUID
}

func newFixture() *fixture {
	filings := libraryapptest.NewMembershipRepository()
	f := &fixture{
		works:     libraryapptest.NewEbookRepository(filings),
		groupings: libraryapptest.NewCollectionRepository(filings),
		filings:   filings,
		marks:     readingapptest.NewAnnotationRepository(),
		positions: readingapptest.NewProgressRepository(),
		reader:    uuid.New(),
		phone:     uuid.New(),
		tablet:    uuid.New(),
	}

	f.reconciler = records.New(&records.Repositories{
		Works:     f.works,
		Groupings: f.groupings,
		Filings:   f.filings,
		Marks:     f.marks,
		Positions: f.positions,
	})

	return f
}

// op builds an operation authored by device at the causal version and instant
// given.
func (f *fixture) op(
	device uuid.UUID,
	entity operation.TargetEntity,
	target uuid.UUID,
	kind operation.Kind,
	at time.Time,
	counter uint64,
	delta operation.Delta,
) *operation.Operation {
	return operation.Restore(uuid.New(), &operation.Props{
		UserID:      f.reader,
		DeviceID:    device,
		Target:      operation.Target{Entity: entity, ID: target},
		Kind:        kind,
		Delta:       delta,
		VectorClock: crdt.VectorClock{crdt.Author(device): counter},
		CreatedAt:   at,
	})
}

// importWork seeds a work the reader already holds, written by the phone.
func (f *fixture) importWork(t *testing.T, title string) *libraryebook.Ebook {
	t.Helper()

	work, err := libraryebook.New(
		f.reader,
		&libraryebook.Details{Title: libraryebook.Title(title)},
		&libraryebook.File{
			Format: libraryebook.FormatEPUB,
			Hash:   libraryebook.ContentHash("0000000000000000000000000000000000000000000000000000000000000001"),
		},
		f.phone,
		authored,
	)
	if err != nil {
		t.Fatalf("seeding a work: %v", err)
	}

	if err = f.works.Create(t.Context(), work); err != nil {
		t.Fatalf("seeding a work: %v", err)
	}

	return work
}

// raw renders a delta value.
func raw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return encoded
}

// reconcile runs the reconciler and fails the test on an error, which is the
// node failing rather than a verdict.
func (f *fixture) reconcile(t *testing.T, op *operation.Operation) operation.Verdict {
	t.Helper()

	got, err := f.reconciler.Reconcile(t.Context(), op)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	return got
}

func TestReconcileInsertsAWorkThisNodeHasNotSeen(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := uuid.New()

	got := f.reconcile(t, f.op(f.phone, operation.TargetEbook, work, operation.KindInsert, authored, 1,
		operation.Delta{
			"title":        raw("Vidas Secas"),
			"author":       raw("Graciliano Ramos"),
			"format":       raw("epub"),
			"content_hash": raw("0000000000000000000000000000000000000000000000000000000000000001"),
		}))

	if got.Outcome != operation.OutcomeApplied {
		t.Fatalf("Reconcile = %s (%s), want applied", got.Outcome, got.Detail)
	}

	stored, err := f.works.GetByID(t.Context(), work)
	if err != nil {
		t.Fatalf("the work was reported applied and not written: %v", err)
	}

	switch {
	case stored.ID != work:
		t.Error("the work was written under an identifier of this node's, which no peer would recognize")
	case stored.Title.String() != "Vidas Secas":
		t.Errorf("the title came out as %q", stored.Title)
	case stored.Revision.DeviceID != f.phone:
		t.Error("the record does not reflect the device whose write it is")
	case !stored.Revision.UpdatedAt.Equal(authored):
		t.Errorf("the record was stamped %s, want the author's %s", stored.Revision.UpdatedAt, authored)
	}
}

// A delta names the fields the change wrote and nothing else, so a field it
// does not name keeps whatever the record already held (RN06).
func TestReconcileWritesOnlyTheFieldsTheDeltaClaims(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	got := f.reconcile(t, f.op(f.tablet, operation.TargetEbook, work.ID, operation.KindUpdate,
		authored.Add(time.Minute), 1, operation.Delta{"author": raw("Graciliano Ramos")}))

	if got.Outcome != operation.OutcomeApplied {
		t.Fatalf("Reconcile = %s (%s), want applied", got.Outcome, got.Detail)
	}

	stored, err := f.works.GetByID(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.Title.String() != "Vidas Secas" {
		t.Errorf("a field the change did not claim came out as %q", stored.Title)
	}

	if stored.Author.String() != "Graciliano Ramos" {
		t.Errorf("the claimed field came out as %q", stored.Author)
	}
}

// The record already holds a version this one does not causally precede and
// does not beat on the tie-break, so the operation is kept in the log and the
// record is left alone.
func TestReconcileReportsALostMergeAsSuperseded(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	// Concurrent with the seeded version, and stamped an hour earlier.
	got := f.reconcile(t, f.op(f.tablet, operation.TargetEbook, work.ID, operation.KindUpdate,
		authored.Add(-time.Hour), 1, operation.Delta{"title": raw("São Bernardo")}))

	if got.Outcome != operation.OutcomeSuperseded {
		t.Fatalf("Reconcile = %s (%s), want superseded", got.Outcome, got.Detail)
	}

	stored, err := f.works.GetByID(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.Title.String() != "Vidas Secas" {
		t.Errorf("a superseded change was applied anyway: the title is now %q", stored.Title)
	}
}

// Deletion is a write like any other: it carries a clock, an instant and the
// device that made it, and reconciles by the rule everything else does.
func TestReconcileAppliesATombstone(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	got := f.reconcile(t, f.op(f.phone, operation.TargetEbook, work.ID, operation.KindDelete,
		authored.Add(time.Minute), 2, nil))

	if got.Outcome != operation.OutcomeApplied {
		t.Fatalf("Reconcile = %s (%s), want applied", got.Outcome, got.Detail)
	}

	stored, err := f.works.GetByID(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if !stored.IsDeleted() {
		t.Error("the tombstone was not written, so the next node to hear about it would resurrect the work")
	}
}

// Only an insert may create a record. An update arriving before the insert it
// depends on means the log reaching this node was already broken, and
// inventing a record out of a partial delta would hide that.
func TestReconcileRefusesAChangeToARecordThisNodeDoesNotHold(t *testing.T) {
	t.Parallel()

	f := newFixture()

	for name, kind := range map[string]operation.Kind{
		"an update":  operation.KindUpdate,
		"a deletion": operation.KindDelete,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := f
			got := f.reconcile(t, f.op(f.phone, operation.TargetEbook, uuid.New(), kind,
				authored, 1, operation.Delta{"title": raw("Vidas Secas")}))

			if got.Outcome != operation.OutcomeRejected {
				t.Errorf("Reconcile = %s, want rejected", got.Outcome)
			}
		})
	}
}

// A peer names the reader in the request and could name a record that is not
// theirs. A node that wrote it would let one authorization reach another
// reader's collection.
func TestReconcileRefusesARecordBelongingToAnotherReader(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	op := f.op(f.phone, operation.TargetEbook, work.ID, operation.KindUpdate,
		authored.Add(time.Minute), 2, operation.Delta{"title": raw("São Bernardo")})
	op.UserID = uuid.New()

	if got := f.reconcile(t, op); got.Outcome != operation.OutcomeRejected {
		t.Errorf("Reconcile = %s, want rejected", got.Outcome)
	}
}

// The federation has to survive one of its nodes learning a kind of record the
// others do not know, and refusing the operation while answering the rest of
// the batch is what surviving it looks like.
func TestReconcileRefusesAnEntityThisNodeDoesNotReplicate(t *testing.T) {
	t.Parallel()

	f := newFixture()

	got := f.reconcile(t, f.op(f.phone, "shelf", uuid.New(), operation.KindInsert, authored, 1,
		operation.Delta{"name": raw("Cabeceira")}))

	if got.Outcome != operation.OutcomeRejected {
		t.Errorf("Reconcile = %s, want rejected", got.Outcome)
	}
}

// A filing carries a surrogate key each replica mints for itself, so two
// devices filing the same work under the same shelf while offline produce two
// identifiers for one record (C18). The pair is what identifies it.
func TestReconcileResolvesAFilingByItsPairAndNotByItsIdentifier(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")
	grouping := uuid.New()

	pair := operation.Delta{"ebook_id": raw(work.ID), "collection_id": raw(grouping)}

	// The phone files the work, minting its own row identifier.
	first := f.reconcile(t, f.op(f.phone, operation.TargetEbookCollection, uuid.New(),
		operation.KindInsert, authored, 1, pair))
	if first.Outcome != operation.OutcomeApplied {
		t.Fatalf("the first filing = %s (%s), want applied", first.Outcome, first.Detail)
	}

	// The tablet, which was offline, files the same pair under an identifier
	// of its own and later.
	second := f.reconcile(t, f.op(f.tablet, operation.TargetEbookCollection, uuid.New(),
		operation.KindDelete, authored.Add(time.Minute), 1, pair))
	if second.Outcome != operation.OutcomeApplied {
		t.Fatalf("the second filing = %s (%s), want applied", second.Outcome, second.Detail)
	}

	filing, err := f.filings.GetByPair(t.Context(), work.ID, grouping)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	if filing.IsFiled() {
		t.Error("the second change wrote a second row instead of the one the pair already had")
	}
}

// A position belongs to one work and one device, and the device is the one
// that authored the change — taking it from anywhere else would let one
// appliance move another's bookmark.
func TestReconcileAddressesAPositionByTheWorkAndTheAuthoringDevice(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	delta := operation.Delta{"ebook_id": raw(work.ID), "locator": raw("epubcfi(/6/14!/4/10/3:10)")}

	if got := f.reconcile(t, f.op(f.phone, operation.TargetReadingProgress, uuid.New(),
		operation.KindInsert, authored, 1, delta)); got.Outcome != operation.OutcomeApplied {
		t.Fatalf("Reconcile = %s (%s), want applied", got.Outcome, got.Detail)
	}

	position, err := f.positions.GetByPair(t.Context(), work.ID, f.phone)
	if err != nil {
		t.Fatalf("the position was reported applied and not written: %v", err)
	}

	if position.Locator.String() != "epubcfi(/6/14!/4/10/3:10)" {
		t.Errorf("the position came out at %q", position.Locator)
	}

	// The same work read on a second device is a second row, never a conflict.
	if got := f.reconcile(t, f.op(f.tablet, operation.TargetReadingProgress, uuid.New(),
		operation.KindInsert, authored.Add(-time.Hour), 1, delta)); got.Outcome != operation.OutcomeApplied {
		t.Errorf("the second device's position = %s (%s), want applied", got.Outcome, got.Detail)
	}
}

// A reader who stops reading a work leaves their position where it was, so a
// change that claims to remove one is refused rather than silently ignored.
func TestReconcileRefusesToRemoveAReadingPosition(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	got := f.reconcile(t, f.op(f.phone, operation.TargetReadingProgress, uuid.New(),
		operation.KindDelete, authored, 1, operation.Delta{"ebook_id": raw(work.ID)}))

	if got.Outcome != operation.OutcomeRejected {
		t.Errorf("Reconcile = %s, want rejected", got.Outcome)
	}
}

// A mark names no reader: reading.annotations references the work, so whose a
// mark is is a fact about the work it is in.
func TestReconcileRefusesAMarkInAWorkThatIsNotTheReadersOwn(t *testing.T) {
	t.Parallel()

	f := newFixture()

	got := f.reconcile(t, f.op(f.phone, operation.TargetAnnotation, uuid.New(),
		operation.KindInsert, authored, 1, operation.Delta{
			"ebook_id": raw(uuid.New()),
			"kind":     raw("note"),
			"locator":  raw("page=42"),
			"text":     raw("a sertão é uma sociedade"),
		}))

	if got.Outcome != operation.OutcomeRejected {
		t.Errorf("Reconcile = %s, want rejected", got.Outcome)
	}
}

func TestReconcileInsertsAMarkInTheReadersOwnWork(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")
	mark := uuid.New()

	got := f.reconcile(t, f.op(f.phone, operation.TargetAnnotation, mark,
		operation.KindInsert, authored, 1, operation.Delta{
			"ebook_id": raw(work.ID),
			"kind":     raw("note"),
			"locator":  raw("page=42"),
			"text":     raw("a sertão é uma sociedade"),
		}))

	if got.Outcome != operation.OutcomeApplied {
		t.Fatalf("Reconcile = %s (%s), want applied", got.Outcome, got.Detail)
	}

	stored, err := f.marks.GetByID(t.Context(), mark)
	if err != nil {
		t.Fatalf("the mark was reported applied and not written: %v", err)
	}

	if stored.Text.String() != "a sertão é uma sociedade" {
		t.Errorf("the note came out as %q", stored.Text)
	}
}

// A value the column would refuse is refused here instead, and reported
// against the field that carried it rather than as a table.
func TestReconcileRefusesADeltaTheRecordCannotHold(t *testing.T) {
	t.Parallel()

	f := newFixture()
	work := f.importWork(t, "Vidas Secas")

	tests := map[string]operation.Delta{
		"a title the column cannot hold":             {"title": raw("")},
		"a value of the wrong kind":                  {"title": raw(42)},
		"an insert missing a field the record needs": {"author": raw("Graciliano Ramos")},
	}

	for name, delta := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			target, kind := work.ID, operation.KindUpdate
			if name == "an insert missing a field the record needs" {
				target, kind = uuid.New(), operation.KindInsert
			}

			got := f.reconcile(t, f.op(f.tablet, operation.TargetEbook, target, kind,
				authored.Add(time.Minute), 1, delta))

			if got.Outcome != operation.OutcomeRejected {
				t.Errorf("Reconcile = %s, want rejected", got.Outcome)
			}

			if got.Detail == "" {
				t.Error("the rejection says nothing, and the operator has nothing to act on")
			}
		})
	}
}

// The reconciler is called from inside the transaction that appended the
// operation, so it must not swallow a failure of the node: a batch that
// reported a reader's history as refused because the database went away would
// be lost rather than retried, and the log would carry a verdict nobody could
// justify.
func TestReconcilePassesOnAFailureOfTheNode(t *testing.T) {
	t.Parallel()

	f := newFixture()

	reconciler := records.New(&records.Repositories{
		Works:     &failingWorks{},
		Groupings: f.groupings,
		Filings:   f.filings,
		Marks:     f.marks,
		Positions: f.positions,
	})

	_, err := reconciler.Reconcile(t.Context(), f.op(f.phone, operation.TargetEbook, uuid.New(),
		operation.KindUpdate, authored, 1, operation.Delta{"title": raw("Vidas Secas")}))
	if err == nil {
		t.Error("Reconcile reported a verdict on a node that could not answer")
	}
}

// failingWorks is a catalogue that has stopped answering, which is what a
// database that went away looks like from here. Its refusal is not one of the
// kinds a rejection is made of, so it must reach the caller as an error.
type failingWorks struct {
	libraryebook.Repository
}

func (*failingWorks) GetByID(context.Context, uuid.UUID) (*libraryebook.Ebook, error) {
	return nil, errs.Wrap(context.Canceled, errs.KindUnavailable, "the catalogue is not answering")
}
