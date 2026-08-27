// Package identifier decodes the identifiers the reading contract carries as
// strings.
//
// It is one package rather than a helper repeated in seven controllers, because
// the answer to a malformed one is a decision and not a parse: a value that is
// not a uuid is answered exactly as one nobody has. The reply must not be an
// oracle for which identifiers exist, and a client that sent a broken one learns
// the same thing either way — that there is nothing there.
//
// Each entity has its own function, and the reason is the code the refusal
// carries: a client that asked about a mark is told there is no such mark, not
// that there is no such work.
package identifier

import (
	"uuid"

	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opAnnotation = "reading/identifier: annotation"
	opEbook      = "reading/identifier: ebook"
)

// Annotation decodes the identifier of a mark.
func Annotation(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindNotFound, "no such annotation").
			WithOp(opAnnotation).
			WithCode(annotation.CodeNotFound)
	}

	return id, nil
}

// Ebook decodes the identifier of a work.
//
// The refusal is the library slice's, taken from the entity that owns it. A
// client that sent a malformed work identifier to this service and to that one
// has to be told the same thing, or the difference is something it could tell
// the two apart by.
func Ebook(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindNotFound, "no such work in the collection").
			WithOp(opEbook).
			WithCode(libraryebook.CodeNotFound)
	}

	return id, nil
}
