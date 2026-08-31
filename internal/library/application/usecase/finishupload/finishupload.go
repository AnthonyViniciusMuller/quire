// Package finishupload is UC02's last call for a caller that cannot open a
// client stream: it ends the session, checks what arrived, and records that
// this node holds it.
//
// Both checks are the streamed shape's checks, made in the same place against
// the same kind of staged file — the session hands over exactly what staging
// produces from a stream, which is what makes the two shapes of UC02 differ in
// how the bytes arrive and in nothing else.
package finishupload

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/application/upload"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
)

// FinishUpload ends upload sessions.
type FinishUpload struct {
	rules   *upload.Rules
	uploads service.Uploads
}

// FinishUpload satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*FinishUpload)(nil)

// New returns the use case over the rules of UC02 and the sessions.
func New(rules *upload.Rules, uploads service.Uploads) *FinishUpload {
	return &FinishUpload{rules: rules, uploads: uploads}
}

// Execute checks the bytes and stores them.
func (f *FinishUpload) Execute(ctx context.Context, input Input) (Output, error) {
	finished, err := f.uploads.Finish(ctx, input.UserID, input.UploadID)
	if err != nil {
		return Output{}, err
	}

	defer func() { _ = finished.Staged.Close() }()

	// Against the declaration the session was begun with, never against one
	// restated here: a caller that could restate it would declare a small file,
	// pass the ceiling, and finish a large one.
	if mismatch := f.rules.Verify(finished.Staged, finished.Declared); mismatch != nil {
		return Output{}, mismatch
	}

	record, alreadyHeld, err := f.rules.Store(ctx, finished.Staged, finished.Declared)
	if err != nil {
		return Output{}, err
	}

	return Output{Content: record, AlreadyHeld: alreadyHeld}, nil
}
