package listannotations

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
)

// Input is one page of what the calling reader wrote in one work.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID

	// EbookID is the work. It is never optional: the work is what establishes
	// whose the marks are.
	EbookID uuid.UUID

	// PageSize is how many marks to return. Zero asks the node to choose,
	// which is what a client with no opinion should send.
	PageSize int

	// Cursor is where to continue from, zero for the first page.
	Cursor annotation.Cursor
}

// Output is the page, and where the next one starts.
type Output struct {
	// Annotations are the marks, in the stable order the repository defines.
	Annotations []*annotation.Annotation
	// NextCursor is where the next page continues from, and is the zero value
	// when this page was the last.
	NextCursor annotation.Cursor
}
