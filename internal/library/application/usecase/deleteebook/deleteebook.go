// Package deleteebook is the delete half of UC01 (RF01).
//
// A tombstone, never a removal. A row deleted outright is resurrected by the
// next node that had not yet heard about the deletion, so deletion is a write:
// it carries a vector clock, a timestamp and the device that made it, and it
// reconciles against a concurrent edit like any other version would.
package deleteebook

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/deleteebook: execute"

// DeleteEbook removes works from a collection.
type DeleteEbook struct {
	works       ebook.Repository
	memberships membership.Repository
	clock       service.Clock
	transaction service.Transaction
}

// DeleteEbook satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*DeleteEbook)(nil)

// New returns the use case over its dependencies.
func New(
	works ebook.Repository,
	memberships membership.Repository,
	clock service.Clock,
	transaction service.Transaction,
) *DeleteEbook {
	return &DeleteEbook{works: works, memberships: memberships, clock: clock, transaction: transaction}
}

// Execute tombstones the work and every filing of it.
//
// The two are one transaction because they are one change to the reader, and
// because they replicate separately. A node that committed the work's tombstone
// without its filings' would show the work on a shelf it is no longer in — here
// until somebody noticed, and on every peer for as long as they had not
// received the missing half.
//
// The file is not touched. The bytes are keyed by their digest and shared:
// another reader on this node may hold the same work, and a second device of
// this reader will ask for it again once the deletion has reached it and been
// undone. Reclaiming an object is a question about every row that names the
// digest, which is an operator's sweep and not a reader's call.
func (d *DeleteEbook) Execute(ctx context.Context, input Input) (Output, error) {
	at := d.clock.Now()

	err := d.transaction.Within(ctx, func(ctx context.Context) error {
		work, err := d.works.GetByID(ctx, input.EbookID)
		if err != nil {
			return err
		}

		// A work already tombstoned is answered as one that does not exist.
		// Stamping it again would claim a write that was not made, and a
		// second deletion has nothing to tell the reader that the first did
		// not.
		if !work.BelongsTo(input.UserID) || work.IsDeleted() {
			return getebook.NotFound(opExecute)
		}

		work.Delete(input.DeviceID, at)

		if err := d.works.Update(ctx, work); err != nil {
			return err
		}

		return d.memberships.ClearForEbook(ctx, work.ID, input.DeviceID, at)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}
