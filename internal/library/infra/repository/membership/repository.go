// Package membership is the PostgreSQL adapter of the filings repository: it
// satisfies the port declared in internal/library/domain/membership and is the
// only place that knows library.ebook_collections exists.
package membership

import (
	"context"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/persist/revision"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate             = "library/membership: create"
	opUpdate             = "library/membership: update"
	opGetByPair          = "library/membership: get by pair"
	opClearForEbook      = "library/membership: clear for ebook"
	opClearForCollection = "library/membership: clear for collection"
)

// constraintPair is the name of the uniqueness rule on the (work, grouping)
// pair, as it appears in the driver error. It is what tells a filing that
// already exists from any other write failure — and the rule itself is C06,
// which Quadro 20 does not have.
const constraintPair = "ebook_collections_pair_key"

// Repository reads and writes what is filed where, in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ membership.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *librarydb.Queries {
	return librarydb.New(r.manager.Executor(ctx))
}

// Create records a filing, naming the pair rule when it was the one broken.
func (r *Repository) Create(ctx context.Context, filing *membership.Membership) error {
	columns := revision.ToColumns(filing.Revision)

	err := r.queries(ctx).CreateMembership(ctx, librarydb.CreateMembershipParams{
		ID:           filing.ID,
		EbookID:      filing.EbookID,
		CollectionID: filing.CollectionID,
		VectorClock:  columns.VectorClock,
		UpdatedAt:    columns.UpdatedAt,
		DeviceID:     columns.DeviceID,
		Deleted:      columns.Deleted,
	})

	if persist.IsUniqueViolation(err, constraintPair) {
		return errs.Wrap(err, errs.KindAlreadyExists, "that work is already filed under that grouping").
			WithOp(opCreate).
			WithCode(membership.CodeAlreadyFiled).
			WithField("ebook_id", "the pair already has a row, whose register the caller should set")
	}

	return persist.Classify(err, opCreate)
}

// Update writes back the register and the revision.
func (r *Repository) Update(ctx context.Context, filing *membership.Membership) error {
	rows, err := r.update(ctx, filing)
	if err != nil {
		return err
	}

	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByPair reads the filing of one work under one grouping, set or cleared.
func (r *Repository) GetByPair(
	ctx context.Context, ebookID, collectionID uuid.UUID,
) (*membership.Membership, error) {
	row, err := r.queries(ctx).GetMembershipByPair(ctx, librarydb.GetMembershipByPairParams{
		EbookID:      ebookID,
		CollectionID: collectionID,
	})
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err, opGetByPair)
		}

		return nil, persist.Classify(err, opGetByPair)
	}

	return toDomain(&row), nil
}

// ClearForEbook tombstones every filing of one work.
func (r *Repository) ClearForEbook(ctx context.Context, ebookID, device uuid.UUID, at time.Time) error {
	rows, err := r.queries(ctx).ListFiledMembershipsForEbook(ctx, ebookID)
	if err != nil {
		return persist.Classify(err, opClearForEbook)
	}

	return r.clear(ctx, rows, device, at, opClearForEbook)
}

// ClearForCollection tombstones every filing under one grouping. The works
// themselves survive it.
func (r *Repository) ClearForCollection(
	ctx context.Context, collectionID, device uuid.UUID, at time.Time,
) error {
	rows, err := r.queries(ctx).ListFiledMembershipsForCollection(ctx, collectionID)
	if err != nil {
		return persist.Classify(err, opClearForCollection)
	}

	return r.clear(ctx, rows, device, at, opClearForCollection)
}

// clear tombstones each row through the entity that owns the stamping rule.
//
// It is a loop and not one UPDATE with a jsonb_set expression, and the reason
// is worth stating: the rule C01 describes — tick the clock, step the
// timestamp past the one the row already carries, record the device — exists
// once, in crdt.Revision, and is what the convergence argument rests on. A SET
// clause that recomputed it would be a second copy of it, in a language where
// it could not be tested against the first.
//
// The cost is one statement per shelf a work was on, or per work on a shelf.
// Both run inside the caller's unit of work, so a failure halfway leaves
// neither the tombstone nor the filings it was clearing.
func (r *Repository) clear(
	ctx context.Context,
	rows []librarydb.LibraryEbookCollection,
	device uuid.UUID,
	at time.Time,
	op string,
) error {
	for index := range rows {
		filing := toDomain(&rows[index])
		filing.Clear(device, at)

		// The row was read as filed a moment ago, so a zero here is a filing
		// something else cleared in between — which is the same answer this
		// call was reaching for, and not a failure of it.
		if _, err := r.update(ctx, filing); err != nil {
			return errs.Wrap(err, errs.KindOf(err), "the filings could not be cleared").WithOp(op)
		}
	}

	return nil
}

// update writes one filing back and reports how many rows it matched.
func (r *Repository) update(ctx context.Context, filing *membership.Membership) (int64, error) {
	columns := revision.ToColumns(filing.Revision)

	rows, err := r.queries(ctx).UpdateMembership(ctx, librarydb.UpdateMembershipParams{
		ID:          filing.ID,
		VectorClock: columns.VectorClock,
		UpdatedAt:   columns.UpdatedAt,
		DeviceID:    columns.DeviceID,
		Deleted:     columns.Deleted,
	})
	if err != nil {
		return 0, persist.Classify(err, opUpdate)
	}

	return rows, nil
}

// notFound is the answer to a pair that has never been written.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "that work is not filed under that grouping").
		WithOp(op).
		WithCode(membership.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one the pair has carried since it was
// first written.
func toDomain(row *librarydb.LibraryEbookCollection) *membership.Membership {
	props := membership.Props{
		EbookID:      row.EbookID,
		CollectionID: row.CollectionID,
		Revision:     revision.FromColumns(row.VectorClock, row.UpdatedAt, row.DeviceID, row.Deleted),
	}

	return membership.Restore(row.ID, &props)
}
