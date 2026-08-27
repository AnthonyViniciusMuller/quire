// Package content is the record that this node holds the bytes of a file: the
// entity, the value objects that describe where they are, and the port a
// repository has to satisfy.
//
// It is an extension to the MER, which models only metadata (D02 in
// docs/tcc-corrections.md). What it adds is the one thing the metadata cannot
// say: whether the file itself is here.
//
// That separation is the whole point of the table. library.ebooks does not
// reference it, and a foreign key would be wrong rather than merely
// unnecessary: a node replicating a reader with replicates_files false holds
// every work row and none of the files, and a reference would make the
// metadata unreplicable there. The presence of a row here means one thing
// only — this node has the bytes.
//
// The key is the digest, which is what deduplicates. Two readers who imported
// the same file, and one reader importing it on two devices, converge on one
// stored object; and because the digest is the name, a byte that changed is a
// different object rather than a corrupted one.
//
// Nothing here replicates. A content record is a statement about this node's
// disk, not about the reader's library, so it carries no vector clock and no
// tombstone — and a peer that receives the work will make its own record when
// and if it fetches the bytes.
package content

import (
	"time"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by the constructor.
const opNew = "library/content: new"

// CodeInvalidContent is a record that could not be written for a reason none
// of the value objects owns.
const CodeInvalidContent = "invalid_content"

// Props is everything about the stored bytes other than their digest.
type Props struct {
	// Size is the length in bytes, which library.ebook_contents requires to be
	// greater than zero: a file of no bytes is not a file.
	Size int64

	// MediaType is what the bytes are, as the client declared them.
	MediaType MediaType

	// Locator is where the object store put them.
	Locator

	// StoredAt is when this node came to hold them.
	StoredAt time.Time
}

// Content is the record that this node holds one file
// (library.ebook_contents).
type Content struct {
	// Hash is the digest of the bytes and the primary key. It is the same
	// value library.ebooks.content_hash carries, which is what lets a work and
	// its file be written, replicated and deleted independently.
	Hash ebook.ContentHash

	Props
}

// New records that this node holds the bytes.
//
// It is called after the transfer and never before it: the row is what a later
// download is answered from, so writing it for bytes that did not arrive would
// make this node claim a file it cannot serve.
func New(hash ebook.ContentHash, size int64, mediaType MediaType, at Locator, storedAt time.Time) (*Content, error) {
	if err := hash.Validate(); err != nil {
		return nil, err
	}

	if err := mediaType.Validate(); err != nil {
		return nil, err
	}

	if err := at.Validate(); err != nil {
		return nil, err
	}

	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the stored file could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidContent).
			WithField(field, reason)
	}

	switch {
	case size <= 0:
		return nil, invalid("size_bytes", "a file of no bytes is not a file")
	case storedAt.IsZero():
		return nil, invalid("created_at", "the record must say when this node came to hold the bytes")
	}

	return &Content{
		Hash: hash,
		Props: Props{
			Size:      size,
			MediaType: mediaType,
			Locator:   at,
			StoredAt:  storedAt,
		},
	}, nil
}

// Restore rebuilds a record already stored.
func Restore(hash ebook.ContentHash, props *Props) *Content {
	return &Content{Hash: hash, Props: *props}
}
