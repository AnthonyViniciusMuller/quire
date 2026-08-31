// Package uploadcontent is UC02 as a stream: it receives the bytes of a work
// this node does not hold yet, in one call, from a client that can hold a
// client stream open (RF04).
//
// Every decision it makes about the file is in
// [github.com/anthonyvsmuller/quire/internal/library/application/upload],
// because UC02 is served by two shapes and the rules are the same rules. What
// is this package's own is the ordering that only a stream has: the bytes are
// received before they are stored, so a node that streamed straight through
// would be writing under a name that promises they are something else — for as
// long as the transfer takes, and permanently if the node died in between.
// Every later reader of that object trusts the name, so the bytes are staged,
// checked, and only then stored.
package uploadcontent

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/application/upload"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
)

// The codes this use case raises, which are the rules' codes: a client tells
// the two shapes of UC02 apart by which call it made, never by what a refusal
// is called.
const (
	// CodeDigestMismatch is bytes that are not what the client said they were.
	CodeDigestMismatch = upload.CodeDigestMismatch
	// CodeSizeMismatch is a length that is not what the client said it was.
	CodeSizeMismatch = upload.CodeSizeMismatch
	// CodeUnclaimedContent is a digest no work of the caller's names, which is
	// C16 in docs/tcc-corrections.md.
	CodeUnclaimedContent = upload.CodeUnclaimedContent
)

// UploadContent receives files that arrive in one stream.
type UploadContent struct {
	rules   *upload.Rules
	staging service.Staging
}

// UploadContent satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UploadContent)(nil)

// New returns the use case over the rules of UC02 and the staging that holds
// the bytes while they are checked.
func New(rules *upload.Rules, staging service.Staging) *UploadContent {
	return &UploadContent{rules: rules, staging: staging}
}

// Execute receives the bytes, checks them, and records that this node holds
// them.
func (u *UploadContent) Execute(ctx context.Context, input Input) (Output, error) {
	admitted, err := u.rules.Admit(ctx, input.UserID, input.ContentHash, input.MediaType, input.Size)
	if err != nil {
		return Output{}, err
	}

	// The node already has these exact bytes. The reply comes before the
	// stream is read, so the transfer does not happen at all.
	if admitted.Held != nil {
		return Output{Content: admitted.Held, AlreadyHeld: true}, nil
	}

	staged, err := u.staging.Stage(ctx, input.Body, u.rules.Limit())
	if err != nil {
		return Output{}, err
	}

	defer func() { _ = staged.Close() }()

	if mismatch := u.rules.Verify(staged, admitted.Declared); mismatch != nil {
		return Output{}, mismatch
	}

	record, alreadyHeld, err := u.rules.Store(ctx, staged, admitted.Declared)
	if err != nil {
		return Output{}, err
	}

	return Output{Content: record, AlreadyHeld: alreadyHeld}, nil
}
