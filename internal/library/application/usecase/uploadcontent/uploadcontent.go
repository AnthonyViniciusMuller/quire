// Package uploadcontent is UC02: it receives the bytes of a work this node
// does not hold yet (RF04).
//
// Two orderings decide everything in this file.
//
// The bytes are received before they are stored. The object is named by its
// digest and the digest is only known once every byte has arrived, so a node
// that streamed straight through would be writing under a name that promises
// the bytes are something else — for as long as the transfer takes, and
// permanently if the node died in between. Every later reader of that object
// trusts the name, so the bytes are staged, checked, and only then stored.
//
// The object is stored before the row that points at it. A failure between the
// two leaves an object nothing points at, which the next upload of the same
// file overwrites; the other order would leave a row promising a file that is
// not there, which nothing repairs and every download believes.
package uploadcontent

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/uploadcontent: execute"

// The stable machine-readable codes this use case raises.
const (
	// CodeDigestMismatch is bytes that are not what the client said they were.
	CodeDigestMismatch = "content_digest_mismatch"
	// CodeSizeMismatch is a length that is not what the client said it was.
	CodeSizeMismatch = "content_size_mismatch"
	// CodeUnclaimedContent is a digest no work of the caller's names, which is
	// C16 in docs/tcc-corrections.md.
	CodeUnclaimedContent = "unclaimed_content"
)

// UploadContent receives files.
type UploadContent struct {
	works    ebook.Repository
	contents content.Repository
	blobs    service.BlobStore
	staging  service.Staging
	clock    service.Clock
	limit    int64
}

// UploadContent satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UploadContent)(nil)

// New returns the use case over its dependencies. The limit is the largest
// file the node will accept.
func New(
	works ebook.Repository,
	contents content.Repository,
	blobs service.BlobStore,
	staging service.Staging,
	clock service.Clock,
	limit int64,
) *UploadContent {
	return &UploadContent{
		works:    works,
		contents: contents,
		blobs:    blobs,
		staging:  staging,
		clock:    clock,
		limit:    limit,
	}
}

// Execute receives the bytes, checks them, and records that this node holds
// them.
func (u *UploadContent) Execute(ctx context.Context, input Input) (Output, error) {
	hash, mediaType, err := u.admit(ctx, &input)
	if err != nil {
		return Output{}, err
	}

	// The node already has these exact bytes — another device of this reader,
	// or another reader here, uploaded them. The reply comes before the stream
	// is read, so the transfer does not happen at all.
	held, err := u.contents.GetByHash(ctx, hash)
	if err == nil {
		return Output{Content: held, AlreadyHeld: true}, nil
	}

	if !errors.Is(err, errs.KindNotFound) {
		return Output{}, err
	}

	staged, err := u.staging.Stage(ctx, input.Body, u.limit)
	if err != nil {
		return Output{}, err
	}

	defer func() { _ = staged.Close() }()

	if err := u.verify(staged, &input, hash); err != nil {
		return Output{}, err
	}

	return u.store(ctx, staged, hash, mediaType)
}

// admit refuses what the node will not receive, before any of it travels.
//
// The declared length is checked against the ceiling here rather than only
// against what arrives, which is the whole reason the contract puts the
// description in its own message: a node that discovered the size by receiving
// it would have received it.
func (u *UploadContent) admit(
	ctx context.Context, input *Input,
) (ebook.ContentHash, content.MediaType, error) {
	hash, err := ebook.ParseContentHash(input.ContentHash)
	if err != nil {
		return "", "", err
	}

	mediaType, err := content.ParseMediaType(input.MediaType)
	if err != nil {
		return "", "", err
	}

	if input.Size <= 0 {
		return "", "", errs.New(errs.KindInvalidArgument, "the upload declares no bytes").
			WithOp(opExecute).
			WithField("size_bytes", "it must say how many bytes will arrive, and a file of none is not a file")
	}

	if input.Size > u.limit {
		return "", "", errs.New(errs.KindResourceExhausted, "the file is larger than this node accepts").
			WithOp(opExecute).
			WithCode(service.CodeUploadTooLarge).
			WithField("size_bytes", "it must be at most what the node was configured to hold")
	}

	// C16: the upload carries no work identifier, because the object is shared
	// between every work that names the digest. Without this check the object
	// store is writable by any authenticated reader, under any name, with no
	// row anywhere saying whose file it was. A correct client always passes
	// it: the flow is CreateEbook, read content_missing, then upload.
	claimed, err := u.works.HoldsContent(ctx, input.UserID, hash)
	if err != nil {
		return "", "", err
	}

	if !claimed {
		return "", "", errs.New(errs.KindFailedPrecondition,
			"no work of yours names that file").
			WithOp(opExecute).
			WithCode(CodeUnclaimedContent).
			WithField("content_hash", "record the work first; the reply says whether the bytes are needed")
	}

	return hash, mediaType, nil
}

// verify reports why the bytes are not what the client said they were, or nil.
//
// The digest is the check that matters and the length is the one that
// explains: a truncated transfer fails both, and telling the client which
// makes the difference between a bug it can find and one it cannot.
func (u *UploadContent) verify(staged service.Staged, input *Input, hash ebook.ContentHash) error {
	if staged.Size() != input.Size {
		return errs.Newf(errs.KindInvalidArgument,
			"the upload declared %d bytes and %d arrived", input.Size, staged.Size()).
			WithOp(opExecute).
			WithCode(CodeSizeMismatch).
			WithField("size_bytes", "the transfer was cut short, or the length was wrong")
	}

	if staged.Digest() != hash.String() {
		return errs.New(errs.KindInvalidArgument, "the bytes are not the file that was declared").
			WithOp(opExecute).
			WithCode(CodeDigestMismatch).
			WithField("content_hash", "the digest of what arrived does not match the one declared")
	}

	return nil
}

// store puts the checked bytes in the object store and records that this node
// holds them.
func (u *UploadContent) store(
	ctx context.Context,
	staged service.Staged,
	hash ebook.ContentHash,
	mediaType content.MediaType,
) (Output, error) {
	if err := staged.Rewind(); err != nil {
		return Output{}, err
	}

	at, err := u.blobs.Put(ctx, &service.Blob{
		Hash:      hash,
		Size:      staged.Size(),
		MediaType: mediaType,
	}, staged)
	if err != nil {
		return Output{}, err
	}

	record, err := content.New(hash, staged.Size(), mediaType, at, u.clock.Now())
	if err != nil {
		return Output{}, err
	}

	err = u.contents.Create(ctx, record)

	switch {
	case err == nil:
		return Output{Content: record}, nil

	// Another upload of the same file finished while this one was streaming.
	// The bytes are identical by construction — the digest is the key — so
	// this is the answer the caller wanted, arrived at by somebody else.
	case errors.Is(err, errs.KindAlreadyExists):
		return Output{Content: record, AlreadyHeld: true}, nil

	default:
		// The object is stored and nothing points at it. Removing it is what
		// keeps a failed upload from costing the node a book's worth of disk
		// for ever; leaving it would be harmless to correctness, since the
		// next upload of the same file writes the same key.
		_ = u.blobs.Remove(ctx, at)

		return Output{}, err
	}
}
