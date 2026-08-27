package content

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// The stable machine-readable codes a repository reports.
const (
	// CodeNotFound is a file this node does not hold.
	//
	// It is a legitimate answer and not only an error. A node that replicates
	// a reader without their files has every work row and none of the bytes,
	// and answering a download with "this node does not hold them" is what the
	// authorization made legitimate (D02).
	CodeNotFound = "content_not_found"
	// CodeAlreadyStored is a digest this node already holds, which is the
	// ordinary outcome of two readers importing the same file.
	CodeAlreadyStored = "content_already_stored"
)

// Repository is the port through which the use cases of the library slice
// record what this node holds. It belongs to the domain; what satisfies it
// lives in internal/library/infra/repository/content.
//
// It says nothing about where the bytes are. That is service.BlobStore, and
// the two are written to in one order and read in the other: the bytes are
// stored first and the row second, so a failure in between leaves an object
// nothing points at rather than a row pointing at nothing.
type Repository interface {
	// Create records that this node holds the bytes, and reports
	// errs.KindAlreadyExists with CodeAlreadyStored when it already did.
	Create(ctx context.Context, stored *Content) error

	// GetByHash reads where the bytes are, and reports errs.KindNotFound when
	// this node does not hold them.
	GetByHash(ctx context.Context, hash ebook.ContentHash) (*Content, error)

	// Has reports whether this node holds the bytes, which is the question
	// UC01 answers a creation with: a client is told to upload only when the
	// answer is no, and it is no far less often than a client might expect.
	Has(ctx context.Context, hash ebook.ContentHash) (bool, error)
}
