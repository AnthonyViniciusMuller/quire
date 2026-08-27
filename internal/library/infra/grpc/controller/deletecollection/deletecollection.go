// Package deletecollection serves LibraryService.DeleteCollection (UC03,
// delete).
package deletecollection

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deletecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// DeleteCollection serves the call.
type DeleteCollection struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(remove command.Usecase[usecase.Input, usecase.Output]) *DeleteCollection {
	return &DeleteCollection{usecase: remove}
}

// Handle tombstones the grouping. The works survive it.
func (c *DeleteCollection) Handle(
	ctx context.Context,
	request *quirev1.DeleteCollectionRequest,
) (*quirev1.DeleteCollectionResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Collection(request.GetCollectionId())
	if err != nil {
		return nil, err
	}

	if _, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:       identity.UserID,
		DeviceID:     identity.DeviceID,
		CollectionID: id,
	}); err != nil {
		return nil, err
	}

	return &quirev1.DeleteCollectionResponse{}, nil
}
