// Package putchunk is UC02's middle call: the bytes, one call at a time.
//
// It makes no decision about the file. What may be received was settled when
// the session was begun, and what the bytes have to be is settled when it is
// finished; this only puts them where the session is holding them, and answers
// with where that now is.
package putchunk

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
)

// PutChunk receives the bytes of an open session.
type PutChunk struct {
	uploads service.Uploads
}

// PutChunk satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*PutChunk)(nil)

// New returns the use case over the sessions.
func New(uploads service.Uploads) *PutChunk {
	return &PutChunk{uploads: uploads}
}

// Execute writes the chunk and reports where the session is.
//
// A chunk at an offset the node is not expecting is not an error: it is not
// written, and the answer carries the offset it does expect. That is what makes
// the upload resumable, and it is the port's rule rather than this use case's.
func (p *PutChunk) Execute(ctx context.Context, input Input) (Output, error) {
	put, err := p.uploads.Append(ctx, input.UserID, input.UploadID, input.Offset, input.Chunk)
	if err != nil {
		return Output{}, err
	}

	return Output{ReceivedBytes: put.Received}, nil
}
