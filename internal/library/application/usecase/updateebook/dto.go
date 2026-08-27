package updateebook

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// Changes is the fields an edit claims.
//
// A nil pointer is a field the mask did not name, and it is left to whichever
// device wrote it last. That is not only a convenience: reconciliation is
// per-field last-writer-wins, so a mask naming two fields is a claim over those
// two and a claim over nothing else — a client that sent the whole record
// would claim every field, and would win against edits from another device it
// had never seen.
//
// A pointer to an empty string is a claim, and it is how a field is cleared. A
// work whose author is unknown and one whose author the reader deleted are the
// same state, and the reader is entitled to reach it.
type Changes struct {
	Title     *string
	Author    *string
	Publisher *string
	Language  *string
	Extra     *map[string]any
}

// IsEmpty reports whether the edit claims nothing at all.
func (c *Changes) IsEmpty() bool {
	return c.Title == nil && c.Author == nil && c.Publisher == nil &&
		c.Language == nil && c.Extra == nil
}

// Input is an edit to the description of one work.
//
// The file is absent from it, and from the contract: UC01 is «CRD» because the
// bytes cannot be edited, and a row whose format or digest changed would
// describe a file it is not.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the edit, from the token. It is what
	// the new revision names.
	DeviceID uuid.UUID
	// EbookID is the work.
	EbookID uuid.UUID
	// Changes is what the edit claims.
	Changes Changes
}

// Output is the work as the collection now holds it.
type Output struct {
	// Ebook is the row that was written.
	Ebook *ebook.Ebook
}
