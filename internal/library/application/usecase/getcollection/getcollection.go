// Package getcollection is the read half of UC03 for one grouping (RF05).
package getcollection

import (
	"context"
	"uuid"

	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/getcollection: execute"

// GetCollection reads one grouping.
type GetCollection struct {
	collections collection.Repository
}

// GetCollection satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*GetCollection)(nil)

// New returns the use case over its dependencies.
func New(collections collection.Repository) *GetCollection {
	return &GetCollection{collections: collections}
}

// Execute reads the grouping, or reports that there is none.
func (g *GetCollection) Execute(ctx context.Context, input Input) (Output, error) {
	grouping, err := g.collections.GetByID(ctx, input.CollectionID)
	if err != nil {
		return Output{}, err
	}

	if err := Visible(grouping, input.UserID, opExecute); err != nil {
		return Output{}, err
	}

	return Output{Collection: grouping}, nil
}

// Visible reports why the reader may not see the grouping, or nil.
//
// It is exported because every call in the slice that names a grouping makes
// this check, and they have to make it the same way: no such identifier, an
// identifier belonging to another reader, and a grouping this reader deleted
// are one answer, and a call that distinguished them would be the oracle the
// check exists to remove.
func Visible(grouping *collection.Collection, userID uuid.UUID, op string) error {
	if grouping.BelongsTo(userID) && !grouping.IsDeleted() {
		return nil
	}

	return NotFound(op)
}

// NotFound is the answer this slice gives for a grouping the caller may not
// see, whichever of the three reasons applies.
func NotFound(op string) error {
	return errs.New(errs.KindNotFound, "no such grouping").
		WithOp(op).
		WithCode(collection.CodeNotFound)
}
