// Package deletecollection is the delete half of UC03 (RF05).
//
// The works survive the grouping. Deleting a shelf is not deleting what was on
// it: what is tombstoned with the grouping are the filings, and every work
// stays in the reader's collection.
package deletecollection

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/deletecollection: execute"

// DeleteCollection removes groupings.
type DeleteCollection struct {
	collections collection.Repository
	memberships membership.Repository
	clock       service.Clock
	transaction service.Transaction
}

// DeleteCollection satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*DeleteCollection)(nil)

// New returns the use case over its dependencies.
func New(
	collections collection.Repository,
	memberships membership.Repository,
	clock service.Clock,
	transaction service.Transaction,
) *DeleteCollection {
	return &DeleteCollection{
		collections: collections,
		memberships: memberships,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute tombstones the grouping and every filing under it.
//
// The row lock is what makes this hold against a work being filed under the
// grouping at the same moment. Both calls read the grouping and then write a
// row that references it, and under READ COMMITTED neither read can see the
// other's write; without the lock the filing would be written against a
// grouping that had been tombstoned in between, and the work would sit on a
// shelf no reply mentions and no later deletion would clear.
func (d *DeleteCollection) Execute(ctx context.Context, input Input) (Output, error) {
	at := d.clock.Now()

	err := d.transaction.Within(ctx, func(ctx context.Context) error {
		grouping, err := d.collections.GetByIDForUpdate(ctx, input.CollectionID)
		if err != nil {
			return err
		}

		// A grouping already tombstoned is answered as one that does not
		// exist. Stamping it again would claim a write that was not made.
		if err := getcollection.Visible(grouping, input.UserID, opExecute); err != nil {
			return err
		}

		grouping.Delete(input.DeviceID, at)

		if err := d.collections.Update(ctx, grouping); err != nil {
			return err
		}

		return d.memberships.ClearForCollection(ctx, grouping.ID, input.DeviceID, at)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}
