// Package getebook is the read half of UC01 for one work (RF01).
package getebook

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/getebook: execute"

// GetEbook reads one work.
type GetEbook struct {
	works ebook.Repository
}

// GetEbook satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*GetEbook)(nil)

// New returns the use case over its dependencies.
func New(works ebook.Repository) *GetEbook { return &GetEbook{works: works} }

// Execute reads the work, or reports that there is none.
//
// Three different situations are answered identically: no such identifier, an
// identifier belonging to another reader, and a work this reader tombstoned. A
// reply that distinguished them would be an oracle for which identifiers exist
// and whose they are, and the client can do nothing different with any of the
// three.
func (g *GetEbook) Execute(ctx context.Context, input Input) (Output, error) {
	work, err := g.works.GetByID(ctx, input.EbookID)
	if err != nil {
		return Output{}, err
	}

	if !work.BelongsTo(input.UserID) || work.IsDeleted() {
		return Output{}, NotFound(opExecute)
	}

	return Output{Ebook: work}, nil
}

// NotFound is the answer this slice gives for a work the caller may not see,
// whichever of the three reasons applies.
//
// It is exported because the use cases that write to a work make the same
// check and have to make it the same way: one of them answering differently
// would be the oracle the check exists to remove.
func NotFound(op string) error {
	return errs.New(errs.KindNotFound, "no such work in the collection").
		WithOp(op).
		WithCode(ebook.CodeNotFound)
}
