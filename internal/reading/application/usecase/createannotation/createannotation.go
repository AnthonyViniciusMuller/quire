// Package createannotation is the create half of UC04 (RF03).
//
// A mark is one row and the call writes one row, so there is no unit of work
// here. What it does first is establish that the reader may write in the work
// at all, which is what everything in this slice hangs off: the mark names the
// work and not the reader.
package createannotation

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
)

// CreateAnnotation records marks.
type CreateAnnotation struct {
	marks annotation.Repository
	works service.Works
	clock service.Clock
}

// CreateAnnotation satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*CreateAnnotation)(nil)

// New returns the use case over its dependencies.
func New(marks annotation.Repository, works service.Works, clock service.Clock) *CreateAnnotation {
	return &CreateAnnotation{marks: marks, works: works, clock: clock}
}

// Execute writes the mark.
//
// The work is checked before anything is parsed, so that a reader writing in
// somebody else's book is told there is no such book rather than which of their
// fields is malformed. The check and the write are not serialized against each
// other, and do not need to be: a work tombstoned in between leaves a mark that
// no call in this slice will ever return, since every one of them establishes
// the reader through the work.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (c *CreateAnnotation) Execute(ctx context.Context, input Input) (Output, error) {
	if err := c.works.Visible(ctx, input.EbookID, input.UserID); err != nil {
		return Output{}, err
	}

	mark, err := parse(&input)
	if err != nil {
		return Output{}, err
	}

	written, err := annotation.New(input.EbookID, mark, input.DeviceID, c.clock.Now())
	if err != nil {
		return Output{}, err
	}

	if err = c.marks.Create(ctx, written); err != nil {
		return Output{}, err
	}

	return Output{Annotation: written}, nil
}

// parse turns what the client sent into the mark the entity takes, refusing
// the first field that cannot be one.
func parse(input *Input) (*annotation.Mark, error) {
	kind, err := annotation.ParseKind(input.Kind)
	if err != nil {
		return nil, err
	}

	place, err := locator.Parse(input.Locator)
	if err != nil {
		return nil, err
	}

	return &annotation.Mark{Kind: kind, Text: annotation.ParseText(input.Text), Locator: place}, nil
}
