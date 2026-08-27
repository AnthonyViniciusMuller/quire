// Package listebooks serves LibraryService.ListEbooks (UC01, read).
package listebooks

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/listebooks"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/pagetoken"
)

// ListEbooks serves the call.
type ListEbooks struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListEbooks {
	return &ListEbooks{usecase: list}
}

// Handle answers with one page of the collection.
//
// The page token is decoded here and encoded again on the way out, which is
// the whole of what a page token is: the cursor the domain works in, in a form
// a client can hold on to without being able to construct one.
func (c *ListEbooks) Handle(
	ctx context.Context,
	request *quirev1.ListEbooksRequest,
) (*quirev1.ListEbooksResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	grouping, err := identifier.OptionalCollection(request.CollectionId)
	if err != nil {
		return nil, err
	}

	cursor, err := pagetoken.Decode(request.GetPageToken())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:       identity.UserID,
		CollectionID: grouping,
		PageSize:     int(request.GetPageSize()),
		Cursor:       cursor,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ListEbooksResponse{
		Ebooks:        convert.Ebooks(output.Ebooks),
		NextPageToken: pagetoken.Encode(output.NextCursor),
	}, nil
}
