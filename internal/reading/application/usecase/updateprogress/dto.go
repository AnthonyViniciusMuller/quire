package updateprogress

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
)

// Input is where the calling device has reached in one work.
//
// The device is not a field of the request and is not one here either: it comes
// from the token. A position belongs to one device and that device is its only
// writer, so a request that could name one would be a request that could move
// another device's bookmark (RN10).
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance whose position this is, from the token. It is
	// half of the row's natural key and the only device that may write it.
	DeviceID uuid.UUID

	// EbookID is the work being read.
	EbookID uuid.UUID

	// Locator is where the reader stopped, in the client's own expression of a
	// position in its own document.
	Locator string

	// Percent is how far through that is, absent when the client could not
	// compute it — a fixed-layout format it can resolve positions in without
	// knowing the whole. Absent and zero are different claims, which is why
	// this is a pointer and the column is nullable.
	Percent *float64
}

// Output is the position as the work now holds it.
type Output struct {
	// Progress is the row that was written.
	Progress *progress.Progress
}
