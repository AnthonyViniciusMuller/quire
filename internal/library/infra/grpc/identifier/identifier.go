// Package identifier decodes the identifiers the library contract carries as
// strings.
//
// It is one package rather than a helper repeated in a dozen controllers,
// because the answer to a malformed one is a decision and not a parse: a value
// that is not a uuid is answered exactly as one nobody has. The reply must not
// be an oracle for which identifiers exist, and a client that sent a broken one
// learns the same thing either way — that there is nothing there.
//
// Each entity has its own function, and the reason is the code the refusal
// carries: a client that asked for a work is told there is no such work, not
// that there is no such grouping.
package identifier

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opEbook      = "library/identifier: ebook"
	opCollection = "library/identifier: collection"
	opUpload     = "library/identifier: upload"
)

// Ebook decodes the identifier of a work.
func Ebook(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindNotFound, "no such work in the collection").
			WithOp(opEbook).
			WithCode(ebook.CodeNotFound)
	}

	return id, nil
}

// Upload decodes the identifier of an upload session.
//
// A malformed one is not here rather than malformed, which is the answer the
// session registry gives to an identifier that is well formed and belongs to
// somebody else: the two cases are indistinguishable to a caller, and that is
// deliberate — a refusal that told them apart would tell a stranger when they
// had guessed a real one.
func Upload(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindNotFound, "this node is not holding that upload").
			WithOp(opUpload).
			WithCode(service.CodeNoSuchUpload)
	}

	return id, nil
}

// Collection decodes the identifier of a grouping.
func Collection(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindNotFound, "no such grouping").
			WithOp(opCollection).
			WithCode(collection.CodeNotFound)
	}

	return id, nil
}

// OptionalEbook decodes an identifier a request may leave out, and answers the
// zero value when it did.
//
// The absence is a filter that was not asked for, and a filter that was asked
// for with a malformed value is still a refusal: a client that sent nonsense
// should be told, rather than served the unfiltered reply it did not ask for.
func OptionalEbook(value *string) (uuid.UUID, error) {
	if value == nil || *value == "" {
		return uuid.UUID{}, nil
	}

	return Ebook(*value)
}

// OptionalCollection is OptionalEbook for a grouping.
func OptionalCollection(value *string) (uuid.UUID, error) {
	if value == nil || *value == "" {
		return uuid.UUID{}, nil
	}

	return Collection(*value)
}
