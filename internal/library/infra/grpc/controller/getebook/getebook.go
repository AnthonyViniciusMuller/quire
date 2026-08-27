// Package getebook serves LibraryService.GetEbook (UC01, read).
package getebook

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// GetEbook serves the call.
type GetEbook struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(get command.Usecase[usecase.Input, usecase.Output]) *GetEbook {
	return &GetEbook{usecase: get}
}

// Handle answers with the work.
func (c *GetEbook) Handle(
	ctx context.Context,
	request *quirev1.GetEbookRequest,
) (*quirev1.GetEbookResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, EbookID: id})
	if err != nil {
		return nil, err
	}

	return &quirev1.GetEbookResponse{Ebook: convert.Ebook(output.Ebook)}, nil
}
