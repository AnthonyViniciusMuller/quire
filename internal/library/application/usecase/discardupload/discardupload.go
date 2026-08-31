// Package discardupload ends an upload the caller is abandoning.
//
// It is not required for correctness: the node ends a session nobody has sent
// to for long enough, which is what covers the client whose network went away.
// It exists so that a client which has given up on purpose — the reader closed
// the tab, chose another file — says so, and the node releases a half-received
// book now rather than at the end of the expiry.
package discardupload

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
)

// DiscardUpload abandons upload sessions.
type DiscardUpload struct {
	uploads service.Uploads
}

// DiscardUpload satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*DiscardUpload)(nil)

// New returns the use case over the sessions.
func New(uploads service.Uploads) *DiscardUpload {
	return &DiscardUpload{uploads: uploads}
}

// Execute releases what the session was holding.
func (d *DiscardUpload) Execute(ctx context.Context, input Input) (Output, error) {
	return Output{}, d.uploads.Discard(ctx, input.UserID, input.UploadID)
}
