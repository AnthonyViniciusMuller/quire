package getcollection

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
)

// Input names one grouping of the calling reader's.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// CollectionID is the grouping.
	CollectionID uuid.UUID
}

// Output is the grouping.
type Output struct {
	// Collection is the row that was read.
	Collection *collection.Collection
}
