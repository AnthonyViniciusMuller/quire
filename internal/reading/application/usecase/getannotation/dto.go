package getannotation

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
)

// Input is one mark, asked for by the reader who may see it.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// AnnotationID is the mark.
	AnnotationID uuid.UUID
}

// Output is the mark as the work holds it.
type Output struct {
	// Annotation is the row that was read.
	Annotation *annotation.Annotation
}
