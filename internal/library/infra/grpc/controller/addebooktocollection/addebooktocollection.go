// Package addebooktocollection serves LibraryService.AddEbookToCollection
// (UC03).
package addebooktocollection

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/addtocollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// AddEbookToCollection serves the call.
type AddEbookToCollection struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(add command.Usecase[usecase.Input, usecase.Output]) *AddEbookToCollection {
	return &AddEbookToCollection{usecase: add}
}

// Handle files the work. Repeating it is not an error, which the contract says
// and the register makes true.
func (c *AddEbookToCollection) Handle(
	ctx context.Context,
	request *quirev1.AddEbookToCollectionRequest,
) (*quirev1.AddEbookToCollectionResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	work, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	grouping, err := identifier.Collection(request.GetCollectionId())
	if err != nil {
		return nil, err
	}

	if _, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:       identity.UserID,
		DeviceID:     identity.DeviceID,
		EbookID:      work,
		CollectionID: grouping,
	}); err != nil {
		return nil, err
	}

	return &quirev1.AddEbookToCollectionResponse{}, nil
}
