// Package beginebookupload serves LibraryService.BeginEbookUpload (UC02).
//
// It is the first of the three calls that serve UC02 for a caller which cannot
// open a client stream, and it is where the description arrives — so it is
// where an oversized or unsupported file is refused, before any of its bytes
// travel. The streamed shape refuses the same file at the same point, in the
// first message of its stream.
package beginebookupload

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/beginupload"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs
// package expects.
const opHandle = "library/beginebookupload: handle"

// BeginEbookUpload serves the call.
type BeginEbookUpload struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(begin command.Usecase[usecase.Input, usecase.Output]) *BeginEbookUpload {
	return &BeginEbookUpload{usecase: begin}
}

// Handle agrees to receive a file, or says why it will not.
func (c *BeginEbookUpload) Handle(
	ctx context.Context,
	request *quirev1.BeginEbookUploadRequest,
) (*quirev1.BeginEbookUploadResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	description := request.GetContent()
	if description == nil {
		return nil, errs.New(errs.KindInvalidArgument, "the upload was not described").
			WithOp(opHandle).
			WithField("content", "it must say the digest, the length and the media type of what will arrive")
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:      identity.UserID,
		ContentHash: description.GetContentHash(),
		Size:        description.GetSizeBytes(),
		MediaType:   description.GetMediaType(),
	})
	if err != nil {
		return nil, err
	}

	// No identifier when there is nothing to send: a caller that was told the
	// node already holds the file has no session to put chunks into.
	answer := &quirev1.BeginEbookUploadResponse{AlreadyHeld: output.AlreadyHeld}
	if output.AlreadyHeld {
		answer.Content = convert.Content(output.Content)

		return answer, nil
	}

	answer.UploadId = output.UploadID.String()

	return answer, nil
}
