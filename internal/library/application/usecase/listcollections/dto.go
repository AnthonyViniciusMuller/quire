package listcollections

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
)

// Input is the calling reader's groupings.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID

	// EbookID narrows the reply to the groupings one work is filed under, and
	// is the zero value for all of them.
	EbookID uuid.UUID
}

// Output is the groupings, ordered by name.
type Output struct {
	// Collections are the rows that were read.
	Collections []*collection.Collection
}
