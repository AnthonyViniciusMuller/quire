package listebooks

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// Input is one page of the calling reader's collection.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID

	// CollectionID narrows the page to one grouping, and is the zero value for
	// the whole collection.
	CollectionID uuid.UUID

	// PageSize is how many works to return. Zero asks the node to choose,
	// which is what a client with no opinion should send.
	PageSize int

	// Cursor is where to continue from, zero for the first page.
	Cursor ebook.Cursor
}

// Output is the page, and where the next one starts.
type Output struct {
	// Ebooks are the works, most recently imported first.
	Ebooks []*ebook.Ebook
	// NextCursor is where the next page continues from, and is the zero value
	// when this page was the last.
	NextCursor ebook.Cursor
}
