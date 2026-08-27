package createannotation

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
)

// Input is a mark a device has just made.
//
// The reader and the device come from the token and never from the request:
// the device keys the first vector clock entry, and one a caller could name
// would let a client author history on an appliance that was never bound
// (RN10).
//
// The revision is not here at all, and the contract says so: the server stamps
// it. A client that could send one could claim to have written before a write
// it has already seen.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance that made the mark, from the token.
	DeviceID uuid.UUID

	// EbookID is the work the mark is in.
	EbookID uuid.UUID

	// Kind is what kind of mark it is: a note, a highlight or a bookmark.
	Kind string
	// Text is what the reader wrote, empty on a highlight or a bookmark they
	// left uncommented and required on a note.
	Text string
	// Locator is the passage, in the client's own expression of a position in
	// its own document.
	Locator string
}

// Output is the mark as the work now holds it.
type Output struct {
	// Annotation is the row that was written.
	Annotation *annotation.Annotation
}
