package collection

import (
	"strings"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseName        = "library/collection: parse name"
	opParseKind        = "library/collection: parse kind"
	opParseDescription = "library/collection: parse description"
)

// The stable machine-readable codes this package attaches to the errors it
// raises.
const (
	// CodeInvalidName is a grouping name being blank or too long.
	CodeInvalidName = "invalid_collection_name"
	// CodeInvalidKind is a grouping that is neither a collection nor a
	// category.
	CodeInvalidKind = "invalid_collection_kind"
	// CodeInvalidDescription is a description longer than the node will hold.
	CodeInvalidDescription = "invalid_collection_description"
)

// The widths library.collections declares. The description is a text column
// and has none, so the bound below is this node's rather than the schema's.
const (
	maxNameLength        = 120
	maxDescriptionLength = 2000
)

// Name is what the reader calls a grouping.
type Name string

// String renders the name.
func (n Name) String() string { return string(n) }

// Validate reports why the name is not usable, or nil. The blank check is the
// one library.collections_name_not_blank makes.
func (n Name) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the name is not usable").
			WithOp(opParseName).
			WithCode(CodeInvalidName).
			WithField("name", reason)
	}

	switch {
	case string(n) == "":
		return invalid("it must not be empty")
	case characterCount(string(n)) > maxNameLength:
		return invalid("it must be at most 120 characters long")
	default:
		return nil
	}
}

// ParseName removes the surrounding space from s and validates the result.
func ParseName(s string) (Name, error) {
	name := Name(strings.TrimSpace(s))
	if err := name.Validate(); err != nil {
		return "", err
	}

	return name, nil
}

// Kind is what a grouping means to the reader.
//
// Collections and categories are the same structure with a different meaning,
// which is what lets RF05 offer both without a second entity. Nothing in the
// node branches on the value; it exists so that a client can present a shelf
// and a subject differently.
type Kind string

// The two meanings library.collections_kind admits.
const (
	// KindCollection is a shelf the reader assembled.
	KindCollection Kind = "collection"
	// KindCategory is a subject they filed a work under.
	KindCategory Kind = "category"
)

// String renders the kind.
func (k Kind) String() string { return string(k) }

// Validate reports why the kind is not usable, or nil.
func (k Kind) Validate() error {
	switch k {
	case KindCollection, KindCategory:
		return nil
	default:
		return errs.New(errs.KindInvalidArgument, "the kind is not usable").
			WithOp(opParseKind).
			WithCode(CodeInvalidKind).
			WithField("kind", "a grouping is either a collection or a category")
	}
}

// ParseKind lowercases s and validates the result. An empty s is the default
// the column carries, which is a collection: a client that says nothing is
// making a shelf.
func ParseKind(s string) (Kind, error) {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	if trimmed == "" {
		return KindCollection, nil
	}

	kind := Kind(trimmed)
	if err := kind.Validate(); err != nil {
		return "", err
	}

	return kind, nil
}

// Description is what the reader wrote about the grouping, absent when they
// wrote nothing.
type Description string

// String renders the description.
func (d Description) String() string { return string(d) }

// IsZero reports whether the reader wrote nothing.
func (d Description) IsZero() bool { return d == "" }

// Validate reports why the description is not usable, or nil.
//
// The column is text and holds anything PostgreSQL can store, so the bound is
// this node's own. It exists because the value travels in every reply that
// lists the reader's shelves, and an unbounded field on a repeated message is
// a reply whose size no client can plan for.
func (d Description) Validate() error {
	if characterCount(string(d)) > maxDescriptionLength {
		return errs.New(errs.KindInvalidArgument, "the description is not usable").
			WithOp(opParseDescription).
			WithCode(CodeInvalidDescription).
			WithField("description", "it must be at most 2000 characters long")
	}

	return nil
}

// ParseDescription removes the surrounding space from s and validates the
// result.
func ParseDescription(s string) (Description, error) {
	description := Description(strings.TrimSpace(s))
	if err := description.Validate(); err != nil {
		return "", err
	}

	return description, nil
}

// characterCount is the length PostgreSQL measures a varchar in: characters,
// not bytes.
func characterCount(s string) int { return utf8.RuneCountInString(s) }
