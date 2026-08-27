package listprogress

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
)

// Input is every device's position in one work.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// EbookID is the work. It is what establishes whose the positions are.
	EbookID uuid.UUID
}

// Output is the positions, ordered by device so that two calls return the same
// list in the same order — which a client diffing against what it already
// showed depends on.
type Output struct {
	// Progress is one row per device the reader has ever opened the work on,
	// and is empty for a work they have not started.
	Progress []*progress.Progress
}
