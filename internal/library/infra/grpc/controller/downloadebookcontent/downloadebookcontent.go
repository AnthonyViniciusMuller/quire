// Package downloadebookcontent serves LibraryService.DownloadEbookContent.
//
// The stream mirrors the upload: one description, then the bytes. A client
// therefore knows the digest and the length before the first chunk arrives,
// which is what lets it verify what it received rather than trust it.
package downloadebookcontent

import (
	"errors"
	"io"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/downloadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs
// package expects.
const opHandle = "library/downloadebookcontent: handle"

// chunkSize is how many bytes travel in one message.
//
// It is well under the four megabytes gRPC will carry by default, because that
// ceiling is on the message and a client that lowered its own would stop being
// able to read at all. Sixty-four kilobytes is also what most object stores
// hand over in one read, so the chunking adds no copying of its own.
const chunkSize = 64 * 1024

// DownloadEbookContent serves the call.
type DownloadEbookContent struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(download command.Usecase[usecase.Input, usecase.Output]) *DownloadEbookContent {
	return &DownloadEbookContent{usecase: download}
}

// Handle streams the bytes back.
//
// Everything that can be refused is refused before the first message: the
// reader, the work, and whether this node holds the file. Once the description
// has been sent the client is receiving a file, and a failure after that point
// is a truncated download it can detect only by the digest it was given first.
func (c *DownloadEbookContent) Handle(
	request *quirev1.DownloadEbookContentRequest,
	stream quirev1.LibraryService_DownloadEbookContentServer,
) error {
	ctx := stream.Context()

	identity, err := authn.Require(ctx)
	if err != nil {
		return err
	}

	id, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, EbookID: id})
	if err != nil {
		return err
	}

	defer func() { _ = output.Body.Close() }()

	if err := stream.Send(&quirev1.DownloadEbookContentResponse{
		Payload: &quirev1.DownloadEbookContentResponse_Content{
			Content: convert.Content(output.Content),
		},
	}); err != nil {
		return sendError(err)
	}

	return c.stream(output.Body, stream)
}

// stream copies the file into as many messages as it takes.
func (c *DownloadEbookContent) stream(
	body io.Reader, stream quirev1.LibraryService_DownloadEbookContentServer,
) error {
	buffer := make([]byte, chunkSize)

	for {
		read, err := body.Read(buffer)

		// Bytes and an error can arrive together, and an io.Reader that
		// returned both has read those bytes: sending them before looking at
		// the error is what the contract of Read asks for.
		if read > 0 {
			if sendErr := stream.Send(&quirev1.DownloadEbookContentResponse{
				Payload: &quirev1.DownloadEbookContentResponse_Chunk{Chunk: buffer[:read]},
			}); sendErr != nil {
				return sendError(sendErr)
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return errs.Wrap(err, errs.KindUnavailable, "the file could not be read in full").
				WithOp(opHandle)
		}
	}
}

// sendError translates a stream the client stopped listening to.
//
// It is almost always a reader who closed the book, and reporting that as a
// failure of this node would fill the logs with alarms about people putting
// their phone down.
func sendError(err error) error {
	return errs.Wrap(err, errs.KindCanceled, "the caller stopped receiving the file").WithOp(opHandle)
}
