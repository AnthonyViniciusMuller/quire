// Package createebook serves LibraryService.CreateEbook (UC01, create).
package createebook

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/createebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
)

// CreateEbook serves the call.
type CreateEbook struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(create command.Usecase[usecase.Input, usecase.Output]) *CreateEbook {
	return &CreateEbook{usecase: create}
}

// Handle records the work.
//
// The reader and the device come from the token and never from the request.
// The revision is ignored if the client sent one, which the contract says it
// will be: a client that could stamp its own would be able to claim it wrote
// before a write it has already seen.
func (c *CreateEbook) Handle(
	ctx context.Context,
	request *quirev1.CreateEbookRequest,
) (*quirev1.CreateEbookResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	work := request.GetEbook()

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:      identity.UserID,
		DeviceID:    identity.DeviceID,
		Title:       work.GetTitle(),
		Author:      work.GetAuthor(),
		Publisher:   work.GetPublisher(),
		Language:    work.GetLanguage(),
		Format:      convert.FormatValue(work.GetFormat()),
		ContentHash: work.GetContentHash(),
		Size:        work.GetSizeBytes(),
		Extra:       work.GetExtraMetadata().AsMap(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.CreateEbookResponse{
		Ebook:          convert.Ebook(output.Ebook),
		ContentMissing: output.ContentMissing,
	}, nil
}
