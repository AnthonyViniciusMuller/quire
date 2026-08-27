// Package removeebookfromcollection serves
// LibraryService.RemoveEbookFromCollection (UC03).
package removeebookfromcollection

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/removefromcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// RemoveEbookFromCollection serves the call.
type RemoveEbookFromCollection struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(remove command.Usecase[usecase.Input, usecase.Output]) *RemoveEbookFromCollection {
	return &RemoveEbookFromCollection{usecase: remove}
}

// Handle clears the register, and is likewise idempotent.
func (c *RemoveEbookFromCollection) Handle(
	ctx context.Context,
	request *quirev1.RemoveEbookFromCollectionRequest,
) (*quirev1.RemoveEbookFromCollectionResponse, error) {
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

	return &quirev1.RemoveEbookFromCollectionResponse{}, nil
}
