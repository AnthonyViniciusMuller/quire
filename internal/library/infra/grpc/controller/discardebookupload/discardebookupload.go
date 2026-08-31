// Package discardebookupload serves LibraryService.DiscardEbookUpload (UC02).
package discardebookupload

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/discardupload"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// DiscardEbookUpload serves the call.
type DiscardEbookUpload struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(discard command.Usecase[usecase.Input, usecase.Output]) *DiscardEbookUpload {
	return &DiscardEbookUpload{usecase: discard}
}

// Handle releases what the upload was holding.
func (c *DiscardEbookUpload) Handle(
	ctx context.Context,
	request *quirev1.DiscardEbookUploadRequest,
) (*quirev1.DiscardEbookUploadResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Upload(request.GetUploadId())
	if err != nil {
		return nil, err
	}

	if _, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, UploadID: id}); err != nil {
		return nil, err
	}

	return &quirev1.DiscardEbookUploadResponse{}, nil
}
