// Package listebooks is the read half of UC01 for the whole collection (RF01,
// RF04), optionally narrowed to one grouping (UC03).
package listebooks

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// ListEbooks reads pages of a collection.
type ListEbooks struct {
	works ebook.Repository
}

// ListEbooks satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListEbooks)(nil)

// New returns the use case over its dependencies.
func New(works ebook.Repository) *ListEbooks { return &ListEbooks{works: works} }

// Execute reads one page.
//
// The grouping is not checked against the reader, and does not need to be: the
// page is scoped to their works, so a grouping belonging to somebody else
// narrows it to nothing rather than to somebody else's shelf. Refusing it
// instead would tell the caller which identifiers exist.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (l *ListEbooks) Execute(ctx context.Context, input Input) (Output, error) {
	works, next, err := l.works.List(ctx, &ebook.Query{
		UserID:       input.UserID,
		CollectionID: input.CollectionID,
		Size:         pageSize(input.PageSize),
		Cursor:       input.Cursor,
	})
	if err != nil {
		return Output{}, err
	}

	return Output{Ebooks: works, NextCursor: next}, nil
}

// pageSize clamps what the client asked for into what the node will assemble.
//
// A size above the ceiling is served at the ceiling rather than refused. The
// client is not wrong to want the whole collection at once, it is only wrong
// about what one reply can carry, and the cursor is how it gets the rest — an
// error here would make a client that asked for too much unable to read
// anything.
func pageSize(asked int) int {
	switch {
	case asked <= 0:
		return ebook.DefaultPageSize
	case asked > ebook.MaxPageSize:
		return ebook.MaxPageSize
	default:
		return asked
	}
}
