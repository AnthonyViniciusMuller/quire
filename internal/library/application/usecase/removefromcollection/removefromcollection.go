// Package removefromcollection takes a work off a grouping (UC03, RF05).
//
// It clears the register that filing the work set, and it is idempotent for
// the same reason that call is: the row is a register, so clearing one that
// was already clear is the state the caller asked for.
//
// A pair that was never written is the one case where nothing is stamped. A
// row created only to be tombstoned would be a claim that this device once
// filed the work, which is a history that did not happen — and the register
// being absent already means the work is not on the shelf.
package removefromcollection

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/removefromcollection: execute"

// RemoveFromCollection unfiles works.
type RemoveFromCollection struct {
	works       ebook.Repository
	collections collection.Repository
	memberships membership.Repository
	clock       service.Clock
	transaction service.Transaction
}

// RemoveFromCollection satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RemoveFromCollection)(nil)

// New returns the use case over its dependencies.
func New(
	works ebook.Repository,
	collections collection.Repository,
	memberships membership.Repository,
	clock service.Clock,
	transaction service.Transaction,
) *RemoveFromCollection {
	return &RemoveFromCollection{
		works:       works,
		collections: collections,
		memberships: memberships,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute clears the register.
//
// Both halves are checked against the reader, for the reason filing them
// checks both: a call that could name one of each would be a way to learn that
// another reader's row exists.
//
// The grouping is read plainly and not under its row lock, unlike the filing
// call. There is nothing here for the lock to protect: a grouping deleted at
// the same moment tombstones this register too, and both writes say the work
// is not on the shelf.
func (r *RemoveFromCollection) Execute(ctx context.Context, input Input) (Output, error) {
	at := r.clock.Now()

	err := r.transaction.Within(ctx, func(ctx context.Context) error {
		if err := r.visible(ctx, &input); err != nil {
			return err
		}

		filing, err := r.memberships.GetByPair(ctx, input.EbookID, input.CollectionID)
		if err != nil {
			// A pair that was never written is already the state the caller
			// asked for, and writing a row in order to tombstone it would
			// claim a filing that never happened.
			if errors.Is(err, errs.KindNotFound) {
				return nil
			}

			return err
		}

		filing.Clear(input.DeviceID, at)

		return r.memberships.Update(ctx, filing)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}

// visible reports why the reader may not touch this pair, or nil.
func (r *RemoveFromCollection) visible(ctx context.Context, input *Input) error {
	work, err := r.works.GetByID(ctx, input.EbookID)
	if err != nil {
		return err
	}

	if !work.BelongsTo(input.UserID) || work.IsDeleted() {
		return getebook.NotFound(opExecute)
	}

	grouping, err := r.collections.GetByID(ctx, input.CollectionID)
	if err != nil {
		return err
	}

	return getcollection.Visible(grouping, input.UserID, opExecute)
}
