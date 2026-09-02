// Package getannotation is the read half of UC04 for one mark (RF03).
package getannotation

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "reading/getannotation: execute"

// GetAnnotation reads one mark.
type GetAnnotation struct {
	marks annotation.Repository
	works service.Works
}

// GetAnnotation satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*GetAnnotation)(nil)

// New returns the use case over its dependencies.
func New(marks annotation.Repository, works service.Works) *GetAnnotation {
	return &GetAnnotation{marks: marks, works: works}
}

// Execute reads the mark, or reports that there is none.
//
// Four situations are answered identically: no such identifier, a mark in
// another reader's work, a mark this reader tombstoned, and a mark in a work
// they deleted. A reply that distinguished them would be an oracle for which
// identifiers exist and whose they are, and the client can do nothing different
// with any of the four.
//
// The mark is read before the work, which is the only order available: the
// request names a mark, and which work it is in is what the row says.
func (g *GetAnnotation) Execute(ctx context.Context, input Input) (Output, error) {
	mark, err := g.marks.GetByID(ctx, input.AnnotationID)
	if err != nil {
		return Output{}, err
	}

	if mark.IsDeleted() {
		return Output{}, NotFound(opExecute)
	}

	if err = g.works.Visible(ctx, mark.EbookID, input.UserID); err != nil {
		return Output{}, NotVisible(opExecute, err)
	}

	return Output{Annotation: mark}, nil
}

// NotFound is the answer this slice gives for a mark the caller may not see,
// whichever of the four reasons applies.
//
// It is exported because the use cases that write to a mark make the same check
// and have to make it the same way: one of them answering differently — with
// the work's refusal rather than the mark's, say — would be the oracle the
// check exists to remove.
func NotFound(op string) error {
	return errs.New(errs.KindNotFound, "no such annotation").
		WithOp(op).
		WithCode(annotation.CodeNotFound)
}

// NotVisible is the answer for a mark whose work the Works port would not
// show: the mark's own refusal when the port refused, and the port's error
// untouched when it failed.
//
// The two have to be told apart. A work that is not there is one of the four
// situations above and is answered as the mark not being there. A library
// that could not be read is a node that is unavailable, and a client told
// the mark does not exist would believe it and stop asking.
func NotVisible(op string, err error) error {
	if errors.Is(err, errs.KindNotFound) {
		return NotFound(op)
	}

	return err
}
