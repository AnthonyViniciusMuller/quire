// Package addtocollection files a work under a grouping (UC03, RF05).
//
// Repeating the call is not an error. The pair is unique — C06, the constraint
// Quadro 20 does not have — and what the row holds is a register that is set,
// not a row that is appended, so filing a work that is already filed reuses
// the row and stamps a write on it.
//
// Stamping is the part worth stating. The call is idempotent to the reader,
// and it is not a no-op to replication: the write happened on this device, and
// a version that did not record it would let an older removal from another
// device win the tie-break it should have lost.
package addtocollection

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
const opExecute = "library/addtocollection: execute"

// AddToCollection files works.
type AddToCollection struct {
	works       ebook.Repository
	collections collection.Repository
	memberships membership.Repository
	clock       service.Clock
	transaction service.Transaction
}

// AddToCollection satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*AddToCollection)(nil)

// New returns the use case over its dependencies.
func New(
	works ebook.Repository,
	collections collection.Repository,
	memberships membership.Repository,
	clock service.Clock,
	transaction service.Transaction,
) *AddToCollection {
	return &AddToCollection{
		works:       works,
		collections: collections,
		memberships: memberships,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute sets the register, creating the row the first time.
//
// Both halves are checked against the reader before anything is written. A
// work or a grouping that is somebody else's is answered as one that does not
// exist — a filing is a fact about two rows, and a call that could name one of
// each would be a way to learn that the other reader's exists.
//
// The grouping is read under its row lock, which is what makes this hold
// against the grouping being deleted at the same moment: that call takes the
// same lock, so the two serialize, and whichever arrives second sees what the
// first committed. Without it the filing would be written against a grouping
// that had been tombstoned in between.
func (a *AddToCollection) Execute(ctx context.Context, input Input) (Output, error) {
	at := a.clock.Now()

	err := a.transaction.Within(ctx, func(ctx context.Context) error {
		if err := a.visible(ctx, &input); err != nil {
			return err
		}

		filing, err := a.memberships.GetByPair(ctx, input.EbookID, input.CollectionID)

		switch {
		case err == nil:
			filing.Set(input.DeviceID, at)

			return a.memberships.Update(ctx, filing)

		case errors.Is(err, errs.KindNotFound):
			created, buildErr := membership.New(input.EbookID, input.CollectionID, input.DeviceID, at)
			if buildErr != nil {
				return buildErr
			}

			return a.memberships.Create(ctx, created)

		default:
			return err
		}
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}

// visible reports why the reader may not file this pair, or nil.
func (a *AddToCollection) visible(ctx context.Context, input *Input) error {
	work, err := a.works.GetByID(ctx, input.EbookID)
	if err != nil {
		return err
	}

	if !work.BelongsTo(input.UserID) || work.IsDeleted() {
		return getebook.NotFound(opExecute)
	}

	grouping, err := a.collections.GetByIDForUpdate(ctx, input.CollectionID)
	if err != nil {
		return err
	}

	return getcollection.Visible(grouping, input.UserID, opExecute)
}
