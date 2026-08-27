// Package deleteebook serves LibraryService.DeleteEbook (UC01, delete).
package deleteebook

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deleteebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// DeleteEbook serves the call.
type DeleteEbook struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(remove command.Usecase[usecase.Input, usecase.Output]) *DeleteEbook {
	return &DeleteEbook{usecase: remove}
}

// Handle tombstones the work.
func (c *DeleteEbook) Handle(
	ctx context.Context,
	request *quirev1.DeleteEbookRequest,
) (*quirev1.DeleteEbookResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	if _, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		DeviceID: identity.DeviceID,
		EbookID:  id,
	}); err != nil {
		return nil, err
	}

	return &quirev1.DeleteEbookResponse{}, nil
}
