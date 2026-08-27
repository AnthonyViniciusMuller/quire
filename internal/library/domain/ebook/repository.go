package ebook

import (
	"context"
	"time"
	"uuid"
)

// The stable machine-readable codes a repository reports.
const (
	// CodeNotFound is no such work in the reader's collection. It is also the
	// answer to a work that is somebody else's and to one that has been
	// tombstoned, because a reply that distinguished them would tell a reader
	// which identifiers exist.
	CodeNotFound = "ebook_not_found"
)

// The bounds a page of works is read within.
const (
	// DefaultPageSize is what a client that asks for no particular size gets.
	DefaultPageSize = 50
	// MaxPageSize is the largest page the node will assemble, whatever was
	// asked for. A reader with ten thousand works and a client that asked for
	// all of them is a request that would be served slowly and then discarded.
	MaxPageSize = 200
)

// Cursor is the position a page of works continues from.
//
// It is a keyset and not an offset, and the difference matters on a collection
// that is being written to while it is being read: an offset skips a work
// whenever an earlier one is imported between two pages, while a keyset
// resumes from a row the client has already seen the neighbour of.
//
// The pair is what makes it total. imported_at is not unique — two works
// imported in the same microsecond are ordinary — so the identifier breaks the
// tie, exactly as the ordering does.
type Cursor struct {
	// ImportedAt is when the last work of the previous page entered the
	// collection.
	ImportedAt time.Time
	// ID is that work's identifier.
	ID uuid.UUID
}

// IsZero reports whether the cursor asks for the first page.
func (c Cursor) IsZero() bool { return c.ImportedAt.IsZero() && c.ID == (uuid.UUID{}) }

// Query is one page of a reader's collection.
type Query struct {
	// UserID is the reader whose collection is being read. It is never
	// optional: there is no call that reads across readers.
	UserID uuid.UUID

	// CollectionID narrows the page to the works filed under one grouping, and
	// is the zero value for the whole collection.
	CollectionID uuid.UUID

	// Size is how many works to return, already clamped by the use case.
	Size int

	// Cursor is where to continue from, zero for the first page.
	Cursor Cursor
}

// Repository is the port through which the use cases of the library slice read
// and write a reader's works. It belongs to the domain; what satisfies it lives
// in internal/library/infra/repository/ebook.
//
// As in every other slice, the context is passed so that a call can join the
// transaction the manager carries, and a work that does not exist is an error
// of kind errs.KindNotFound rather than a zero value.
type Repository interface {
	// Create records a work.
	Create(ctx context.Context, work *Ebook) error

	// Update writes back the description and the revision the entity stamped.
	//
	// It writes the tombstone as well, because a deletion is a write like any
	// other here. What it never writes is the file: the format, the digest and
	// the length are fixed at import, and a statement that changed them would
	// make the row describe a file it is not.
	Update(ctx context.Context, work *Ebook) error

	// GetByID reads a work by primary key, tombstoned or not.
	//
	// The tombstone is returned rather than hidden, because the caller is the
	// one that knows what it is for: a reader asking to see the work is
	// answered that there is none, and a reader asking to delete it again is
	// answered that it is already gone — and only one of those is an error.
	GetByID(ctx context.Context, id uuid.UUID) (*Ebook, error)

	// HoldsContent reports whether the reader has any work naming the digest,
	// tombstoned or not.
	//
	// It is what an upload is checked against. The bytes are keyed by their
	// digest and shared between every work that names them, so the upload
	// carries no work identifier and the node would otherwise have nothing to
	// decide by — any authenticated reader could stream any bytes under any
	// digest, and the object store would be writable by anyone with an
	// account. C16 in docs/tcc-corrections.md is the finding.
	//
	// Tombstoned works count. A reader who deleted a work on one device and is
	// still uploading its file from another is describing an ordinary
	// crossing, not an attempt to store something they have no claim to.
	HoldsContent(ctx context.Context, userID uuid.UUID, hash ContentHash) (bool, error)

	// List reads one page of a reader's collection, most recently imported
	// first, and returns the cursor the next page continues from — zero when
	// there is no next page.
	//
	// Tombstoned works are never listed. A client rebuilding its local state
	// needs them and gets them from the sync service, which is where the
	// history lives; this call answers what the collection is, not how it got
	// that way.
	List(ctx context.Context, query *Query) ([]*Ebook, Cursor, error)
}
