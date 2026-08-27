// Package updatecollection serves LibraryService.UpdateCollection (UC03,
// update).
package updatecollection

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/updatecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/fieldmask"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// The paths the mask may name.
const (
	pathName        = "name"
	pathKind        = "kind"
	pathDescription = "description"
)

// UpdateCollection serves the call.
type UpdateCollection struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateCollection {
	return &UpdateCollection{usecase: update}
}

// Handle applies the fields the mask named.
func (c *UpdateCollection) Handle(
	ctx context.Context,
	request *quirev1.UpdateCollectionRequest,
) (*quirev1.UpdateCollectionResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Collection(request.GetCollectionId())
	if err != nil {
		return nil, err
	}

	claimed, err := fieldmask.Claimed(request.GetUpdateMask(), pathName, pathKind, pathDescription)
	if err != nil {
		return nil, err
	}

	grouping := request.GetCollection()
	changes := usecase.Changes{}

	if claimed[pathName] {
		name := grouping.GetName()
		changes.Name = &name
	}

	if claimed[pathKind] {
		kind := convert.KindValue(grouping.GetKind())
		changes.Kind = &kind
	}

	if claimed[pathDescription] {
		description := grouping.GetDescription()
		changes.Description = &description
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:       identity.UserID,
		DeviceID:     identity.DeviceID,
		CollectionID: id,
		Changes:      changes,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateCollectionResponse{Collection: convert.Collection(output.Collection)}, nil
}
