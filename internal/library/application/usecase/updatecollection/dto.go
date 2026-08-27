package updatecollection

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
)

// Changes is the fields an edit claims.
//
// A nil pointer is a field the mask did not name, and it is left to whichever
// device wrote it last, for the reason an edit to a work leaves one: the
// reconciliation is per-field, so a client that sent the whole record would
// claim every field and would win against edits it had never seen.
type Changes struct {
	Name        *string
	Kind        *string
	Description *string
}

// IsEmpty reports whether the edit claims nothing at all.
func (c *Changes) IsEmpty() bool {
	return c.Name == nil && c.Kind == nil && c.Description == nil
}

// Input is an edit to one grouping.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the edit, from the token.
	DeviceID uuid.UUID
	// CollectionID is the grouping.
	CollectionID uuid.UUID
	// Changes is what the edit claims.
	Changes Changes
}

// Output is the grouping as the reader's collection now holds it.
type Output struct {
	// Collection is the row that was written.
	Collection *collection.Collection
}
