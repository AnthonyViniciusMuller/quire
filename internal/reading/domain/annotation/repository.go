package annotation

import (
	"context"
	"uuid"
)

// CodeNotFound is no such mark. It is also the answer to a mark in somebody
// else's work and to one that has been tombstoned, because a reply that
// distinguished them would tell a reader which identifiers exist.
const CodeNotFound = "annotation_not_found"

// The bounds a page of marks is read within. They are the library slice's,
// deliberately: a client that has learned what a page of works costs should
// not have to learn a second number for a page of notes.
const (
	// DefaultPageSize is what a client that asks for no particular size gets.
	DefaultPageSize = 50
	// MaxPageSize is the largest page the node will assemble, whatever was
	// asked for.
	MaxPageSize = 200
)

// Cursor is the position a page of marks continues from.
//
// It is the identifier alone, and that is the whole of the ordering as well.
// The row has no immutable value to sort by — Quadro 22 gives an annotation no
// creation instant, and updated_at is rewritten by every edit — so a page
// ordered by anything else would skip or repeat a mark that was edited between
// two requests, which is the one failure keyset pagination exists to remove.
//
// The order is therefore stable rather than meaningful, and it is not this
// node's to make meaningful: where a mark sits in a book is a property of the
// document, which the client can resolve and the server cannot. What this call
// guarantees is that a client which walks every page sees every mark exactly
// once, which is what it needs in order to sort them itself.
type Cursor struct {
	// ID is the last mark of the previous page.
	ID uuid.UUID
}

// IsZero reports whether the cursor asks for the first page.
func (c Cursor) IsZero() bool { return c.ID == (uuid.UUID{}) }

// Query is one page of the marks in one work.
type Query struct {
	// EbookID is the work being read. It is never optional: there is no call
	// that reads marks across works, because the work is what establishes who
	// the marks belong to.
	EbookID uuid.UUID

	// Size is how many marks to return, already clamped by the use case.
	Size int

	// Cursor is where to continue from, zero for the first page.
	Cursor Cursor
}

// Repository is the port through which the use cases of the reading slice read
// and write what a reader has written in a work. It belongs to the domain;
// what satisfies it lives in internal/reading/infra/repository/annotation.
//
// As in every other slice, the context is passed so that a call can join the
// transaction the manager carries, and a mark that does not exist is an error
// of kind errs.KindNotFound rather than a zero value.
type Repository interface {
	// Create records a mark.
	Create(ctx context.Context, mark *Annotation) error

	// Update writes back the mark and the revision the entity stamped. It
	// writes the tombstone as well, because a deletion is a write like any
	// other here.
	Update(ctx context.Context, mark *Annotation) error

	// GetByID reads a mark by primary key, tombstoned or not.
	//
	// The tombstone is returned rather than hidden, because the caller is the
	// one that knows what it is for: a reader asking to see the mark is
	// answered that there is none, and a reader asking to delete it again is
	// answered that it is already gone — and only one of those is an error.
	GetByID(ctx context.Context, id uuid.UUID) (*Annotation, error)

	// List reads one page of the marks in one work, and returns the cursor the
	// next page continues from — zero when there is no next page.
	//
	// Tombstoned marks are never listed. A client rebuilding its local state
	// needs them and gets them from the sync service, which is where the
	// history lives; this call answers what the reader has written, not what
	// they once wrote.
	List(ctx context.Context, query *Query) ([]*Annotation, Cursor, error)
}
