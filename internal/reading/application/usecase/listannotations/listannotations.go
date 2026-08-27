// Package listannotations is the read half of UC04 for everything written in
// one work (RF03).
package listannotations

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
)

// ListAnnotations reads pages of what a reader wrote in a work.
type ListAnnotations struct {
	marks annotation.Repository
	works service.Works
}

// ListAnnotations satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListAnnotations)(nil)

// New returns the use case over its dependencies.
func New(marks annotation.Repository, works service.Works) *ListAnnotations {
	return &ListAnnotations{marks: marks, works: works}
}

// Execute reads one page.
//
// Unlike a page of works, this one has to check the work first. A page of a
// collection is scoped to the reader by the statement itself, so a grouping
// belonging to somebody else narrows it to nothing; a page of marks is scoped
// by the work, and a work belonging to somebody else would return their notes.
func (l *ListAnnotations) Execute(ctx context.Context, input Input) (Output, error) {
	if err := l.works.Visible(ctx, input.EbookID, input.UserID); err != nil {
		return Output{}, err
	}

	marks, next, err := l.marks.List(ctx, &annotation.Query{
		EbookID: input.EbookID,
		Size:    pageSize(input.PageSize),
		Cursor:  input.Cursor,
	})
	if err != nil {
		return Output{}, err
	}

	return Output{Annotations: marks, NextCursor: next}, nil
}

// pageSize clamps what the client asked for into what the node will assemble.
//
// A size above the ceiling is served at the ceiling rather than refused. The
// client is not wrong to want every note at once, it is only wrong about what
// one reply can carry, and the cursor is how it gets the rest — an error here
// would make a client that asked for too much unable to read anything.
func pageSize(asked int) int {
	switch {
	case asked <= 0:
		return annotation.DefaultPageSize
	case asked > annotation.MaxPageSize:
		return annotation.MaxPageSize
	default:
		return asked
	}
}
