package deleteebook

import "uuid"

// Input names the work to remove from the calling reader's collection.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the deletion, from the token. It is
	// what the tombstone names, because a deletion is a write like any other.
	DeviceID uuid.UUID
	// EbookID is the work.
	EbookID uuid.UUID
}

// Output is empty: a deletion has nothing to report but its success.
type Output struct{}
