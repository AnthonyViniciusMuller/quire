package getebook

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// Input names one work in the calling reader's collection.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// EbookID is the work.
	EbookID uuid.UUID
}

// Output is the work.
type Output struct {
	// Ebook is the row that was read.
	Ebook *ebook.Ebook
}
