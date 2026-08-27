package removefromcollection

import "uuid"

// Input takes one work off one grouping.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the change, from the token.
	DeviceID uuid.UUID
	// EbookID is the work.
	EbookID uuid.UUID
	// CollectionID is the grouping.
	CollectionID uuid.UUID
}

// Output is empty, for the reason filing a work reports nothing: the call is
// idempotent.
type Output struct{}
