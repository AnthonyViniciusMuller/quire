// Package listcollections is the read half of UC03 for every grouping (RF05).
package listcollections

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
)

// ListCollections reads a reader's groupings.
type ListCollections struct {
	collections collection.Repository
}

// ListCollections satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListCollections)(nil)

// New returns the use case over its dependencies.
func New(collections collection.Repository) *ListCollections {
	return &ListCollections{collections: collections}
}

// Execute reads them.
//
// It is not paginated, unlike a collection of works: a reader defines shelves
// by hand and there are as many of them as they had patience for. The contract
// says the same, and a page token added later would be a change a client can
// ignore.
//
// The work is not checked against the reader, and does not need to be: the
// reply is scoped to their groupings, so a work belonging to somebody else
// narrows it to nothing rather than to somebody else's shelves.
func (l *ListCollections) Execute(ctx context.Context, input Input) (Output, error) {
	groupings, err := l.collections.List(ctx, input.UserID, input.EbookID)
	if err != nil {
		return Output{}, err
	}

	return Output{Collections: groupings}, nil
}
