// Package createcollection serves LibraryService.CreateCollection (UC03,
// create).
package createcollection

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/createcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
)

// CreateCollection serves the call.
type CreateCollection struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(create command.Usecase[usecase.Input, usecase.Output]) *CreateCollection {
	return &CreateCollection{usecase: create}
}

// Handle defines the grouping.
func (c *CreateCollection) Handle(
	ctx context.Context,
	request *quirev1.CreateCollectionRequest,
) (*quirev1.CreateCollectionResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	grouping := request.GetCollection()

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:      identity.UserID,
		DeviceID:    identity.DeviceID,
		Name:        grouping.GetName(),
		Kind:        convert.KindValue(grouping.GetKind()),
		Description: grouping.GetDescription(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.CreateCollectionResponse{
		Collection: convert.Collection(output.Collection),
	}, nil
}
