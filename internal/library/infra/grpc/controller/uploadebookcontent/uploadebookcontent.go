// Package uploadebookcontent serves LibraryService.UploadEbookContent (UC02).
//
// The stream has a shape the contract fixes: exactly one description, then as
// many chunks as the client chooses. This controller enforces that shape and
// nothing else — it turns the chunks into an io.Reader and hands them to the
// use case, which is where every decision about them is made.
package uploadebookcontent

import (
	"context"
	"errors"
	"io"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/uploadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs
// package expects.
const opHandle = "library/uploadebookcontent: handle"

// CodeMalformedStream is a stream that did not begin with a description, or
// that sent a second one.
const CodeMalformedStream = "malformed_upload_stream"

// UploadEbookContent serves the call.
type UploadEbookContent struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(upload command.Usecase[usecase.Input, usecase.Output]) *UploadEbookContent {
	return &UploadEbookContent{usecase: upload}
}

// Handle receives the file.
//
// The description is read before the use case is called, which is what lets an
// oversized or unsupported file be refused before any of the bytes travel —
// the contract puts it in its own message for exactly that.
func (c *UploadEbookContent) Handle(stream quirev1.LibraryService_UploadEbookContentServer) error {
	ctx := stream.Context()

	identity, err := authn.Require(ctx)
	if err != nil {
		return err
	}

	described, err := stream.Recv()
	if err != nil {
		return receiveError(err)
	}

	description := described.GetContent()
	if description == nil {
		return errs.New(errs.KindInvalidArgument, "the upload did not begin with a description").
			WithOp(opHandle).
			WithCode(CodeMalformedStream).
			WithField("content", "the first message of the stream must describe the file")
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:      identity.UserID,
		ContentHash: description.GetContentHash(),
		Size:        description.GetSizeBytes(),
		MediaType:   description.GetMediaType(),
		Body:        &chunks{stream: stream},
	})
	if err != nil {
		return err
	}

	return stream.SendAndClose(&quirev1.UploadEbookContentResponse{
		Content: convert.Content(output.Content),
	})
}

// chunks is the rest of the stream, as an io.Reader.
//
// It buffers exactly one message at a time. A client chooses its own chunk
// size and the reader above chooses its own buffer, so the two never agree,
// and holding the remainder is what makes a small read from a large message
// work without either side being told about the other.
type chunks struct {
	stream    quirev1.LibraryService_UploadEbookContentServer
	remaining []byte
	done      bool
}

// chunks satisfies what the use case takes.
var _ io.Reader = (*chunks)(nil)

// Read hands over the next bytes of the file.
func (c *chunks) Read(p []byte) (int, error) {
	for len(c.remaining) == 0 {
		if c.done {
			return 0, io.EOF
		}

		message, err := c.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.done = true

				return 0, io.EOF
			}

			return 0, receiveError(err)
		}

		// A second description is refused rather than skipped. The contract
		// says the description arrives exactly once, and a client sending two
		// is a client whose second one this node would silently ignore.
		if message.GetContent() != nil {
			return 0, errs.New(errs.KindInvalidArgument, "the upload described the file twice").
				WithOp(opHandle).
				WithCode(CodeMalformedStream).
				WithField("content", "only the first message of the stream may be a description")
		}

		c.remaining = message.GetChunk()
	}

	read := copy(p, c.remaining)
	c.remaining = c.remaining[read:]

	return read, nil
}

// receiveError translates a stream that stopped into the vocabulary of the
// node.
//
// A cancelled context is the client hanging up, which is not this node's
// failure and must not be logged as one; anything else is a transfer that did
// not arrive.
func receiveError(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return errs.Wrap(err, errs.KindInvalidArgument, "the upload ended before it described the file").
			WithOp(opHandle).
			WithCode(CodeMalformedStream)
	case errors.Is(err, context.Canceled):
		return errs.Wrap(err, errs.KindCanceled, "the caller stopped sending the file").WithOp(opHandle)
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(err, errs.KindDeadlineExceeded, "the upload ran past its deadline").WithOp(opHandle)
	default:
		return errs.Wrap(err, errs.KindUnavailable, "the upload did not arrive in full").WithOp(opHandle)
	}
}
