package createcollection

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
)

// Input is a grouping the reader is defining.
type Input struct {
	// UserID is the reader defining it, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance they defined it on, from the token.
	DeviceID uuid.UUID

	// Name is what they call it.
	Name string
	// Kind is whether it is a shelf or a subject. An empty string is a shelf,
	// which is what a client that says nothing is making.
	Kind string
	// Description is what they wrote about it.
	Description string
}

// Output is the grouping as the reader's collection now holds it.
type Output struct {
	// Collection is the row that was written.
	Collection *collection.Collection
}
