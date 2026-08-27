// Package deleteannotation is the delete half of UC04 (RF03).
//
// A tombstone, never a removal. A row deleted outright is resurrected by the
// next node that had not yet heard about the deletion, so deletion is a write:
// it carries a vector clock, a timestamp and the device that made it, and it
// reconciles against a concurrent edit like any other version would.
package deleteannotation

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "reading/deleteannotation: execute"

// DeleteAnnotation removes marks from a work.
type DeleteAnnotation struct {
	marks annotation.Repository
	works service.Works
	clock service.Clock
}

// DeleteAnnotation satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*DeleteAnnotation)(nil)

// New returns the use case over its dependencies.
func New(marks annotation.Repository, works service.Works, clock service.Clock) *DeleteAnnotation {
	return &DeleteAnnotation{marks: marks, works: works, clock: clock}
}

// Execute tombstones the mark.
//
// A mark already tombstoned is answered as one that does not exist. Stamping it
// again would claim a write that was not made, and a second deletion has
// nothing to tell the reader that the first did not.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (d *DeleteAnnotation) Execute(ctx context.Context, input Input) (Output, error) {
	mark, err := d.marks.GetByID(ctx, input.AnnotationID)
	if err != nil {
		return Output{}, err
	}

	if mark.IsDeleted() {
		return Output{}, getannotation.NotFound(opExecute)
	}

	if err = d.works.Visible(ctx, mark.EbookID, input.UserID); err != nil {
		return Output{}, getannotation.NotFound(opExecute)
	}

	mark.Delete(input.DeviceID, d.clock.Now())

	if err = d.marks.Update(ctx, mark); err != nil {
		return Output{}, err
	}

	return Output{}, nil
}
