package createebook

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// Input is the metadata of a work a device has imported.
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
	// UserID is the reader whose collection the work joins.
	UserID uuid.UUID
	// DeviceID is the appliance that imported it.
	DeviceID uuid.UUID

	// The description, as the client read it out of the file.
	Title     string
	Author    string
	Publisher string
	Language  string

	// The file, which is fixed from here on.
	Format      string
	ContentHash string
	Size        int64

	// Extra is the metadata the format carried and the contract does not name
	// (RF05).
	Extra map[string]any
}

// Output is the work as the collection now holds it.
type Output struct {
	// Ebook is the row that was written.
	Ebook *ebook.Ebook

	// ContentMissing is true when this node does not hold the bytes for the
	// digest declared, which is the client's cue to upload them.
	//
	// It is false far more often than a client might expect: the same file
	// imported on a second device, or by a second reader hosted here, is
	// already stored, because the object is keyed by its digest and not by who
	// imported it.
	ContentMissing bool
}
