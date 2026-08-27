// Package updatecollection is the update half of UC03 (RF05).
package updatecollection

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/updatecollection: execute"

// CodeEmptyUpdate is an edit that claims no field at all.
const CodeEmptyUpdate = "empty_update"

// UpdateCollection edits groupings.
type UpdateCollection struct {
	collections collection.Repository
	clock       service.Clock
}

// UpdateCollection satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateCollection)(nil)

// New returns the use case over its dependencies.
func New(collections collection.Repository, clock service.Clock) *UpdateCollection {
	return &UpdateCollection{collections: collections, clock: clock}
}

// Execute applies the fields the mask named and stamps the write.
//
// An edit that claims nothing is refused rather than served as a no-op, for
// the reason an edit to a work is: it would stamp a revision, and a version
// claiming a write nobody made would win a tie-break against a real edit from
// a device that had been offline.
func (u *UpdateCollection) Execute(ctx context.Context, input Input) (Output, error) {
	if input.Changes.IsEmpty() {
		return Output{}, errs.New(errs.KindInvalidArgument, "the edit claims no field").
			WithOp(opExecute).
			WithCode(CodeEmptyUpdate).
			WithField("update_mask", "it must name at least one field to write")
	}

	grouping, err := u.collections.GetByID(ctx, input.CollectionID)
	if err != nil {
		return Output{}, err
	}

	if visibility := getcollection.Visible(grouping, input.UserID, opExecute); visibility != nil {
		return Output{}, visibility
	}

	details, err := apply(&grouping.Details, &input.Changes)
	if err != nil {
		return Output{}, err
	}

	if err := grouping.Edit(details, input.DeviceID, u.clock.Now()); err != nil {
		return Output{}, err
	}

	if err := u.collections.Update(ctx, grouping); err != nil {
		return Output{}, err
	}

	return Output{Collection: grouping}, nil
}

// apply builds the description the write stores: the fields the mask named,
// over the ones the row already carries.
func apply(current *collection.Details, changes *Changes) (*collection.Details, error) {
	updated := *current

	if changes.Name != nil {
		name, err := collection.ParseName(*changes.Name)
		if err != nil {
			return nil, err
		}

		updated.Name = name
	}

	if changes.Kind != nil {
		kind, err := collection.ParseKind(*changes.Kind)
		if err != nil {
			return nil, err
		}

		updated.Kind = kind
	}

	if changes.Description != nil {
		description, err := collection.ParseDescription(*changes.Description)
		if err != nil {
			return nil, err
		}

		updated.Description = description
	}

	return &updated, nil
}
