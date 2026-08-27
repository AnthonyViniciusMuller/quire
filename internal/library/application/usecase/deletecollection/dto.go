package deletecollection

import "uuid"

// Input names the grouping to remove.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the deletion, from the token. It is
	// what the tombstone names.
	DeviceID uuid.UUID
	// CollectionID is the grouping.
	CollectionID uuid.UUID
}

// Output is empty: a deletion has nothing to report but its success.
type Output struct{}
