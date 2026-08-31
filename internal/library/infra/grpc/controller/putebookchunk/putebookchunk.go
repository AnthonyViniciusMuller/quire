// Package putebookchunk serves LibraryService.PutEbookChunk (UC02).
package putebookchunk

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/putchunk"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs
// package expects.
const opHandle = "library/putebookchunk: handle"

// PutEbookChunk serves the call.
type PutEbookChunk struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(put command.Usecase[usecase.Input, usecase.Output]) *PutEbookChunk {
	return &PutEbookChunk{usecase: put}
}

// Handle writes the chunk and answers with where the upload is.
func (c *PutEbookChunk) Handle(
	ctx context.Context,
	request *quirev1.PutEbookChunkRequest,
) (*quirev1.PutEbookChunkResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Upload(request.GetUploadId())
	if err != nil {
		return nil, err
	}

	// A negative offset is refused here rather than in the session, because it
	// is not a caller that has fallen behind — it is one whose arithmetic is
	// wrong, and answering it with an offset to continue from would hide that.
	if request.GetOffset() < 0 {
		return nil, errs.New(errs.KindInvalidArgument, "the chunk has no place in the file").
			WithOp(opHandle).
			WithField("offset", "it counts from the first byte of the file and cannot be negative")
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		UploadID: id,
		Offset:   request.GetOffset(),
		Chunk:    request.GetChunk(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.PutEbookChunkResponse{ReceivedBytes: output.ReceivedBytes}, nil
}
