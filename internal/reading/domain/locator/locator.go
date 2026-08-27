// Package locator is a place in a document, expressed so that it survives the
// format: a CFI in an EPUB, a page in a PDF, a frame in a comic book archive.
//
// It is a package of its own rather than a type one of the two entities owns,
// and that is a departure from the layout every other slice follows — see
// docs/architecture.md. Both entities of this slice hold one and neither owns
// it: an annotation is attached to a passage and a reading position is a
// passage, which is the same question about the same document asked twice.
// Giving it to either package would make the other import a neighbour for a
// value that is not about it, and giving each a copy would be one validation
// rule in two places, which is the shape of a rule that drifts.
//
// What the node does with the value is nothing. It is never parsed, never
// compared and never ordered here: what a locator means is a property of the
// document, which the client has and the server does not. This node stores it,
// replicates it and hands it back — and the only thing it is entitled to say
// about one is that it is present and that it fits the column.
package locator

import (
	"strings"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opParse is the operation reported by this file, in the form the errs package
// expects.
const opParse = "reading/locator: parse"

// CodeInvalidLocator is a place in a document that is blank or too long.
const CodeInvalidLocator = "invalid_locator"

// maxLength is the width reading.annotations and reading.progress both
// declare.
const maxLength = 255

// Locator is a place in a document.
type Locator string

// String renders the place.
func (l Locator) String() string { return string(l) }

// Validate reports why the place is not usable, or nil.
//
// The blank check is reading.progress_locator_not_blank and
// reading.annotations_locator_not_blank, which are the same constraint written
// on two tables. A record that says where the reader is without saying where
// is a record nothing can be resumed from.
//
// It is written as the constraint is written, over the trimmed value rather
// than over the empty string. A locator of three spaces is blank to the
// database, so a check that admitted it would accept a value the row is then
// refused for — and the refusal would arrive from the driver, in the
// vocabulary of a constraint name rather than of a field a client can point
// at.
func (l Locator) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the place in the document is not usable").
			WithOp(opParse).
			WithCode(CodeInvalidLocator).
			WithField("locator", reason)
	}

	switch {
	case strings.TrimSpace(string(l)) == "":
		return invalid("it must say where in the document this is")
	case utf8.RuneCountInString(string(l)) > maxLength:
		return invalid("it must be at most 255 characters long")
	default:
		return nil
	}
}

// Parse removes the surrounding space from s and validates the result.
//
// Only the surrounding space. What is inside is the client's expression of a
// position in its own document, and a node that normalized it would be
// rewriting a value it cannot interpret.
func Parse(s string) (Locator, error) {
	value := Locator(strings.TrimSpace(s))
	if err := value.Validate(); err != nil {
		return "", err
	}

	return value, nil
}
