// Package getcollection serves LibraryService.GetCollection (UC03, read).
package getcollection

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// GetCollection serves the call.
type GetCollection struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(get command.Usecase[usecase.Input, usecase.Output]) *GetCollection {
	return &GetCollection{usecase: get}
}

// Handle answers with the grouping.
func (c *GetCollection) Handle(
	ctx context.Context,
	request *quirev1.GetCollectionRequest,
) (*quirev1.GetCollectionResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Collection(request.GetCollectionId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, CollectionID: id})
	if err != nil {
		return nil, err
	}

	return &quirev1.GetCollectionResponse{Collection: convert.Collection(output.Collection)}, nil
}
