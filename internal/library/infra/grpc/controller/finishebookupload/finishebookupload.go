// Package finishebookupload serves LibraryService.FinishEbookUpload (UC02).
package finishebookupload

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/finishupload"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// FinishEbookUpload serves the call.
type FinishEbookUpload struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(finish command.Usecase[usecase.Input, usecase.Output]) *FinishEbookUpload {
	return &FinishEbookUpload{usecase: finish}
}

// Handle ends the upload and answers with the file as this node holds it.
func (c *FinishEbookUpload) Handle(
	ctx context.Context,
	request *quirev1.FinishEbookUploadRequest,
) (*quirev1.FinishEbookUploadResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Upload(request.GetUploadId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, UploadID: id})
	if err != nil {
		return nil, err
	}

	return &quirev1.FinishEbookUploadResponse{
		Content:     convert.Content(output.Content),
		AlreadyHeld: output.AlreadyHeld,
	}, nil
}
