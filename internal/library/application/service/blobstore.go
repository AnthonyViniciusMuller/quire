package service

import (
	"context"
	"io"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// The stable machine-readable codes a blob store reports.
const (
	// CodeBlobNotFound is an object the store does not have.
	//
	// It is not the same answer as content.CodeNotFound, which is this node
	// having no record of the file. This one is the record existing and the
	// object being gone, which is an operator's problem rather than a
	// replication state — a bucket emptied, a lifecycle rule, a wrong bucket
	// in the configuration.
	CodeBlobNotFound = "blob_not_found"
	// CodeBlobUnavailable is the object store not answering.
	CodeBlobUnavailable = "blob_store_unavailable"
)

// keyPrefix is the folder every object of this node lives under, so that a
// bucket shared with anything else stays legible.
const keyPrefix = "ebooks/"

// Blob is what a caller asks the store to hold.
type Blob struct {
	// Hash is the digest of the bytes, and the name they are stored under.
	Hash ebook.ContentHash
	// Size is how many bytes will arrive. It is declared rather than
	// discovered because every one of these stores wants a length in advance:
	// without one they buffer the whole file to find it.
	Size int64
	// MediaType is what the bytes are, recorded so that a download can say so.
	MediaType content.MediaType
}

// BlobStore is where the bytes of a work live.
//
// It is one port with three adapters — S3, MinIO and Cloud Storage — chosen by
// which section of the configuration the deployment filled in. The port is
// what makes that a decision in internal/library/di and nowhere else: nothing
// above it names a provider, and the row that records where an object went
// carries the bucket, so a node moved between them can still read what it
// stored under the old one.
//
// Everything here streams. A work is a file a reader chose, which is to say a
// size this node does not get to bound, and an interface that took a []byte
// would decide that every upload is held in memory twice.
type BlobStore interface {
	// Put stores the bytes and returns where they went.
	//
	// It is called after the digest has been verified against what arrived,
	// never before: the key is the digest, so storing unverified bytes would
	// put them under a name that promises they are something else — and every
	// later reader of that object would trust the name.
	//
	// Storing a digest the store already holds is not an error. The bytes are
	// the same bytes, by construction, so the write is a no-op that costs a
	// transfer rather than a conflict that needs resolving.
	Put(ctx context.Context, blob *Blob, body io.Reader) (content.Locator, error)

	// Open reads the bytes back. The caller closes what it returns.
	Open(ctx context.Context, at content.Locator) (io.ReadCloser, error)

	// Remove deletes the object.
	//
	// It exists for the failure in the middle: bytes that were stored and then
	// could not be recorded leave an object nothing points at, and a node that
	// never removed one would accumulate them at the size of a book each. It
	// is never called because a reader deleted a work — a file two readers
	// hold is a file one of them deleting must not take from the other.
	Remove(ctx context.Context, at content.Locator) error

	// Bucket names where this store puts things, which is what a node reports
	// in its configuration and what the row records alongside the key.
	Bucket() string
}

// ObjectKey is the name a digest is stored under.
//
// It lives beside the port rather than in one of the three adapters because
// all three have to agree: a bucket written by a node configured for MinIO and
// then read by the same node configured for the S3 API of the same store is
// the ordinary way a deployment moves, and a second layout would make those
// objects unreachable rather than merely differently named.
//
// The two levels of fan-out are for the filesystem-backed stores. S3 has
// partitioned by key prefix on its own since 2018 and does not need them; a
// MinIO backed by a single directory does, because a million entries in one of
// them is slow on every filesystem the node might be deployed on.
func ObjectKey(hash ebook.ContentHash) string {
	digest := hash.String()
	if len(digest) < 4 {
		return keyPrefix + digest
	}

	return keyPrefix + digest[0:2] + "/" + digest[2:4] + "/" + digest
}
