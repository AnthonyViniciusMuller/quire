package updateannotation

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
)

// Changes is the fields an edit claims.
//
// A nil pointer is a field the mask did not name, and it is left to whichever
// device wrote it last. That is not only a convenience: reconciliation is
// per-field last-writer-wins, so a mask naming two fields is a claim over those
// two and a claim over nothing else — a client that sent the whole record would
// claim every field, and would win against edits from another device it had
// never seen.
//
// A pointer to an empty string is a claim, and it is how the text of a
// highlight is cleared. On a note it is a refusal instead, because a note with
// nothing in it is not a note — the reader who wants one is asking for a
// highlight, and the mask can say so by naming the kind as well.
type Changes struct {
	Kind    *string
	Text    *string
	Locator *string
}

// IsEmpty reports whether the edit claims nothing at all.
func (c *Changes) IsEmpty() bool {
	return c.Kind == nil && c.Text == nil && c.Locator == nil
}

// Input is an edit to one mark.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the edit, from the token. It is what
	// the new revision names, and after this write it is what the row means by
	// "the device whose write this reflects" (C10).
	DeviceID uuid.UUID
	// AnnotationID is the mark.
	AnnotationID uuid.UUID
	// Changes is what the edit claims.
	Changes Changes
}

// Output is the mark as the work now holds it.
type Output struct {
	// Annotation is the row that was written.
	Annotation *annotation.Annotation
}
