// Package listcollections serves LibraryService.ListCollections (UC03, read).
package listcollections

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/listcollections"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// ListCollections serves the call.
type ListCollections struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListCollections {
	return &ListCollections{usecase: list}
}

// Handle answers with the reader's groupings.
func (c *ListCollections) Handle(
	ctx context.Context,
	request *quirev1.ListCollectionsRequest,
) (*quirev1.ListCollectionsResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	work, err := identifier.OptionalEbook(request.EbookId)
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, EbookID: work})
	if err != nil {
		return nil, err
	}

	return &quirev1.ListCollectionsResponse{
		Collections: convert.Collections(output.Collections),
	}, nil
}
