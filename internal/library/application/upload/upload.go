// Package upload is the rules of UC02, in one place, for the two shapes that
// serve it.
//
// A file reaches this node in one of two ways. It is streamed, which is what
// every client that can hold a client stream open does; or it arrives a chunk
// at a time across several unary calls, which is what a browser has to do
// because gRPC-Web carries no client stream (D10, D11). They differ in how the
// bytes arrive and in nothing else, and this package is what makes that true
// rather than merely intended.
//
// Three rules, in the order they are applied, and each has to hold for both
// shapes:
//
// The declaration is admitted before any bytes travel. The length is checked
// against the node's ceiling, the media type against what the node stores, and
// the digest against C16's precondition — that a work of the caller's already
// names it — so that the object store cannot be written to by an account that
// holds no library. A node that discovered the size by receiving it would have
// received it.
//
// What arrived is checked against what was declared. The digest is the check
// that matters and the length is the one that explains.
//
// And the bytes are stored before the row that points at them. A failure
// between the two leaves an object nothing points at, which the next upload of
// the same file overwrites; the other order would leave a row promising a file
// that is not there, which nothing repairs and every download believes.
package upload

import (
	"context"
	"errors"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opRules is the operation reported by this file, in the form the errs package
// expects.
const opRules = "library/upload: rules"

// The stable machine-readable codes the rules of UC02 raise.
const (
	// CodeDigestMismatch is bytes that are not what the client said they were.
	CodeDigestMismatch = "content_digest_mismatch"
	// CodeSizeMismatch is a length that is not what the client said it was.
	CodeSizeMismatch = "content_size_mismatch"
	// CodeUnclaimedContent is a digest no work of the caller's names, which is
	// C16 in docs/tcc-corrections.md.
	CodeUnclaimedContent = "unclaimed_content"
)

// Admitted is a declaration this node has agreed to receive.
type Admitted struct {
	// Declared is what the caller said it was sending, parsed and bounded.
	Declared service.Declared

	// Held is the record this node already has for the digest, and nil when it
	// has none.
	//
	// It is not an error and the caller need do nothing about it: the digest
	// is the key, so the bytes here are the bytes it was about to send —
	// another device of this reader, or another reader on this node, sent them
	// already. Both shapes answer with it before a transfer begins.
	Held *content.Content
}

// Rules applies what UC02 requires of a file, whichever way it arrives.
type Rules struct {
	works    ebook.Repository
	contents content.Repository
	blobs    service.BlobStore
	clock    service.Clock
	limit    int64
}

// New returns the rules over their dependencies. The limit is the largest file
// the node will accept.
func New(
	works ebook.Repository,
	contents content.Repository,
	blobs service.BlobStore,
	clock service.Clock,
	limit int64,
) *Rules {
	return &Rules{works: works, contents: contents, blobs: blobs, clock: clock, limit: limit}
}

// Limit is the largest file the node accepts, which the shapes that stage
// bytes bound their holders with.
func (r *Rules) Limit() int64 { return r.limit }

// Admit refuses what the node will not receive, before any of it travels.
func (r *Rules) Admit(
	ctx context.Context, reader uuid.UUID, declaredHash, declaredMediaType string, size int64,
) (*Admitted, error) {
	hash, err := ebook.ParseContentHash(declaredHash)
	if err != nil {
		return nil, err
	}

	mediaType, err := content.ParseMediaType(declaredMediaType)
	if err != nil {
		return nil, err
	}

	if size <= 0 {
		return nil, errs.New(errs.KindInvalidArgument, "the upload declares no bytes").
			WithOp(opRules).
			WithField("size_bytes", "it must say how many bytes will arrive, and a file of none is not a file")
	}

	if size > r.limit {
		return nil, errs.New(errs.KindResourceExhausted, "the file is larger than this node accepts").
			WithOp(opRules).
			WithCode(service.CodeUploadTooLarge).
			WithField("size_bytes", "it must be at most what the node was configured to hold")
	}

	// C16: the upload carries no work identifier, because the object is shared
	// between every work that names the digest. Without this check the object
	// store is writable by any authenticated reader, under any name, with no
	// row anywhere saying whose file it was. A correct client always passes
	// it: the flow is CreateEbook, read content_missing, then upload.
	claimed, err := r.works.HoldsContent(ctx, reader, hash)
	if err != nil {
		return nil, err
	}

	if !claimed {
		return nil, errs.New(errs.KindFailedPrecondition, "no work of yours names that file").
			WithOp(opRules).
			WithCode(CodeUnclaimedContent).
			WithField("content_hash", "record the work first; the reply says whether the bytes are needed")
	}

	admitted := &Admitted{
		Declared: service.Declared{Hash: hash, Size: size, MediaType: mediaType},
	}

	held, err := r.contents.GetByHash(ctx, hash)

	switch {
	case err == nil:
		admitted.Held = held
	case !errors.Is(err, errs.KindNotFound):
		return nil, err
	}

	return admitted, nil
}

// Verify reports why the bytes are not what the caller said they were, or nil.
//
// The digest is the check that matters and the length is the one that
// explains: a truncated transfer fails both, and telling the client which makes
// the difference between a bug it can find and one it cannot.
func (r *Rules) Verify(staged service.Staged, declared service.Declared) error {
	if staged.Size() != declared.Size {
		return errs.Newf(errs.KindInvalidArgument,
			"the upload declared %d bytes and %d arrived", declared.Size, staged.Size()).
			WithOp(opRules).
			WithCode(CodeSizeMismatch).
			WithField("size_bytes", "the transfer was cut short, or the length was wrong")
	}

	if staged.Digest() != declared.Hash.String() {
		return errs.New(errs.KindInvalidArgument, "the bytes are not the file that was declared").
			WithOp(opRules).
			WithCode(CodeDigestMismatch).
			WithField("content_hash", "the digest of what arrived does not match the one declared")
	}

	return nil
}

// Store puts the checked bytes in the object store and records that this node
// holds them, reporting whether somebody else had got there first.
func (r *Rules) Store(
	ctx context.Context, staged service.Staged, declared service.Declared,
) (record *content.Content, alreadyHeld bool, err error) {
	if rewound := staged.Rewind(); rewound != nil {
		return nil, false, rewound
	}

	at, err := r.blobs.Put(ctx, &service.Blob{
		Hash:      declared.Hash,
		Size:      staged.Size(),
		MediaType: declared.MediaType,
	}, staged)
	if err != nil {
		return nil, false, err
	}

	record, err = content.New(declared.Hash, staged.Size(), declared.MediaType, at, r.clock.Now())
	if err != nil {
		return nil, false, err
	}

	err = r.contents.Create(ctx, record)

	switch {
	case err == nil:
		return record, false, nil

	// Another upload of the same file finished while this one was arriving.
	// The bytes are identical by construction — the digest is the key — so
	// this is the answer the caller wanted, arrived at by somebody else.
	case errors.Is(err, errs.KindAlreadyExists):
		return record, true, nil

	default:
		// The object is stored and nothing points at it. Removing it is what
		// keeps a failed upload from costing the node a book's worth of disk
		// for ever; leaving it would be harmless to correctness, since the
		// next upload of the same file writes the same key.
		_ = r.blobs.Remove(ctx, at)

		return nil, false, err
	}
}
