package annotation

import (
	"strings"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseKind = "reading/annotation: parse kind"
	opParseText = "reading/annotation: parse text"
)

// The stable machine-readable codes this package attaches to the errors it
// raises.
const (
	// CodeInvalidKind is a kind of mark this node cannot name.
	CodeInvalidKind = "invalid_annotation_kind"
	// CodeInvalidText is a note with nothing written in it.
	CodeInvalidText = "invalid_annotation_text"
)

// Kind is what kind of mark the reader left.
//
// A note is text the reader wrote; a highlight and a bookmark are about the
// passage and carry text only if they were given one. Nothing in the node
// branches on the value beyond that one rule, so the three are one column and
// not three tables.
type Kind string

// The kinds reading.annotations_kind admits.
const (
	KindNote      Kind = "note"
	KindHighlight Kind = "highlight"
	KindBookmark  Kind = "bookmark"
)

// kinds is the set above, in the order the contract enumerates it.
//
// It is a function rather than a package-level slice because the project's own
// linter forbids the second, and because a set that cannot be appended to from
// somewhere else is a set with one definition.
func kinds() []Kind { return []Kind{KindNote, KindHighlight, KindBookmark} }

// String renders the kind.
func (k Kind) String() string { return string(k) }

// Validate reports why the kind is not usable, or nil.
//
// It is checked against a closed set for the reason a format is: the wire
// carries it as an enum, so a value outside the set could not have come from a
// client of this contract, and the column refuses it in any case.
//
// Replication is not held to this. A row arriving from a node running a later
// version keeps whatever kind it names — that path writes rows rather than
// calling this constructor — which is what makes adding a kind a change that
// does not break the federation.
func (k Kind) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the kind of mark is not usable").
			WithOp(opParseKind).
			WithCode(CodeInvalidKind).
			WithField("kind", reason)
	}

	if k == "" {
		return invalid("it must say what kind of mark this is")
	}

	for _, known := range kinds() {
		if k == known {
			return nil
		}
	}

	return invalid("this node knows note, highlight and bookmark")
}

// ParseKind lowercases s and validates the result.
func ParseKind(s string) (Kind, error) {
	kind := Kind(strings.ToLower(strings.TrimSpace(s)))
	if err := kind.Validate(); err != nil {
		return "", err
	}

	return kind, nil
}

// Text is what the reader wrote, absent when they wrote nothing.
//
// The column is text and not a varchar, and no bound is imposed here either.
// The specification sets none, a marginal note is as long as the reader made
// it, and the ceiling that does exist is the transport's: a message larger
// than the gRPC receive limit never reaches this type. A limit invented here
// would be a number no requirement asks for, refusing a note somebody has
// already written.
type Text string

// String renders the text.
func (t Text) String() string { return string(t) }

// IsZero reports whether the mark carries no text, which is what the nullable
// column holds as NULL.
func (t Text) IsZero() bool { return t == "" }

// IsBlank reports whether the text says nothing, which is a wider question
// than IsZero and the one reading.annotations_note_has_text asks: the
// constraint tests btrim(text), so a note of three spaces is a note of nothing
// to the database. Checking it the same way here is what keeps a mark the
// entity accepted from being refused by the row.
func (t Text) IsBlank() bool { return strings.TrimSpace(string(t)) == "" }

// Validate reports why the text is not usable, or nil.
//
// On its own it never is: any text is text, and absence is what a highlight
// without a comment looks like. Whether absence is admissible depends on the
// kind, which is a rule about the pair and is checked by [Mark.Validate].
func (t Text) Validate() error { return nil }

// ParseText removes the surrounding space from s.
//
// The trimming is what makes reading.annotations_note_has_text and this
// package agree: the constraint tests btrim(text), so a note of three spaces
// is a note of nothing to the database, and it has to be a note of nothing
// here too or the row would be refused after the entity accepted it.
func ParseText(s string) Text { return Text(strings.TrimSpace(s)) }

// emptyNote is the answer to a note that says nothing, which
// reading.annotations_note_has_text refuses.
func emptyNote() error {
	return errs.New(errs.KindInvalidArgument, "the note is empty").
		WithOp(opParseText).
		WithCode(CodeInvalidText).
		WithField("text", "a note must say something; a highlight and a bookmark need not")
}
