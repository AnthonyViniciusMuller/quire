// Package beginupload is UC02's first call for a caller that cannot open a
// client stream: it agrees to receive a file, and opens the session the bytes
// will arrive into.
//
// Every check it makes is the one the streamed shape makes, in the same place
// and in the same order, because both call
// [github.com/anthonyvsmuller/quire/internal/library/application/upload]. What
// is this package's own is that the agreement outlives the call: a stream
// checks the description and then reads the bytes on the same connection, and
// this has to hand back something the next call can name.
package beginupload

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/application/upload"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
)

// BeginUpload opens upload sessions.
type BeginUpload struct {
	rules   *upload.Rules
	uploads service.Uploads
}

// BeginUpload satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*BeginUpload)(nil)

// New returns the use case over the rules of UC02 and the sessions the bytes
// arrive into.
func New(rules *upload.Rules, uploads service.Uploads) *BeginUpload {
	return &BeginUpload{rules: rules, uploads: uploads}
}

// Execute agrees to receive a file, or says why it will not.
func (b *BeginUpload) Execute(ctx context.Context, input Input) (Output, error) {
	admitted, err := b.rules.Admit(ctx, input.UserID, input.ContentHash, input.MediaType, input.Size)
	if err != nil {
		return Output{}, err
	}

	// The node already has these exact bytes, so no session is opened and
	// nothing is sent. A caller that began anyway would spend a transfer on a
	// file the node would discard at the end.
	if admitted.Held != nil {
		return Output{Content: admitted.Held, AlreadyHeld: true}, nil
	}

	began, err := b.uploads.Begin(ctx, input.UserID, admitted.Declared)
	if err != nil {
		return Output{}, err
	}

	return Output{UploadID: began.ID}, nil
}
