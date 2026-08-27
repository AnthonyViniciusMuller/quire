package ebook

import (
	"strings"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseTitle     = "library/ebook: parse title"
	opParseAuthor    = "library/ebook: parse author"
	opParsePublisher = "library/ebook: parse publisher"
	opParseLanguage  = "library/ebook: parse language"
	opParseFormat    = "library/ebook: parse format"
	opParseHash      = "library/ebook: parse content hash"
	opParseSize      = "library/ebook: parse size"
)

// The stable machine-readable codes this package attaches to the errors it
// raises.
const (
	// CodeInvalidTitle is a title being blank or too long.
	CodeInvalidTitle = "invalid_title"
	// CodeInvalidAuthor is an author being too long.
	CodeInvalidAuthor = "invalid_author"
	// CodeInvalidPublisher is a publisher being too long.
	CodeInvalidPublisher = "invalid_publisher"
	// CodeInvalidLanguage is a language tag being too long.
	CodeInvalidLanguage = "invalid_language"
	// CodeInvalidFormat is a format this node cannot name.
	CodeInvalidFormat = "invalid_format"
	// CodeInvalidContentHash is a digest that is not lowercase hex sha-256.
	CodeInvalidContentHash = "invalid_content_hash"
	// CodeInvalidSize is a declared length of zero or less.
	CodeInvalidSize = "invalid_size"
)

// The widths library.ebooks declares.
const (
	maxTitleLength     = 255
	maxAuthorLength    = 255
	maxPublisherLength = 255
	maxLanguageLength  = 20
)

// Title is what the work is called. It is the one bibliographic field a work
// cannot go without: library.ebooks_title_not_blank refuses the alternative,
// because a row with nothing to show is a row a reader cannot pick out of
// their own collection.
type Title string

// String renders the title.
func (t Title) String() string { return string(t) }

// Validate reports why the title is not usable, or nil.
func (t Title) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the title is not usable").
			WithOp(opParseTitle).
			WithCode(CodeInvalidTitle).
			WithField("title", reason)
	}

	switch {
	case string(t) == "":
		return invalid("it must not be empty")
	case characterCount(string(t)) > maxTitleLength:
		return invalid("it must be at most 255 characters long")
	default:
		return nil
	}
}

// ParseTitle removes the surrounding space from s and validates the result.
func ParseTitle(s string) (Title, error) {
	title := Title(strings.TrimSpace(s))
	if err := title.Validate(); err != nil {
		return "", err
	}

	return title, nil
}

// Author is who wrote the work, and is absent rather than empty when the file
// did not say. The contract makes the same distinction, and it is a real one:
// a work whose author is unknown and one whose author is the empty string are
// different claims, and only the first is worth offering to correct.
type Author string

// String renders the author.
func (a Author) String() string { return string(a) }

// IsZero reports whether the file said nothing about the author.
func (a Author) IsZero() bool { return a == "" }

// Validate reports why the author is not usable, or nil. An absent author is
// usable: the column is nullable.
func (a Author) Validate() error {
	if characterCount(string(a)) > maxAuthorLength {
		return errs.New(errs.KindInvalidArgument, "the author is not usable").
			WithOp(opParseAuthor).
			WithCode(CodeInvalidAuthor).
			WithField("author", "it must be at most 255 characters long")
	}

	return nil
}

// ParseAuthor removes the surrounding space from s and validates the result.
func ParseAuthor(s string) (Author, error) {
	author := Author(strings.TrimSpace(s))
	if err := author.Validate(); err != nil {
		return "", err
	}

	return author, nil
}

// Publisher is who issued the work, absent for the same reason an author is.
type Publisher string

// String renders the publisher.
func (p Publisher) String() string { return string(p) }

// IsZero reports whether the file said nothing about the publisher.
func (p Publisher) IsZero() bool { return p == "" }

// Validate reports why the publisher is not usable, or nil.
func (p Publisher) Validate() error {
	if characterCount(string(p)) > maxPublisherLength {
		return errs.New(errs.KindInvalidArgument, "the publisher is not usable").
			WithOp(opParsePublisher).
			WithCode(CodeInvalidPublisher).
			WithField("publisher", "it must be at most 255 characters long")
	}

	return nil
}

// ParsePublisher removes the surrounding space from s and validates the result.
func ParsePublisher(s string) (Publisher, error) {
	publisher := Publisher(strings.TrimSpace(s))
	if err := publisher.Validate(); err != nil {
		return "", err
	}

	return publisher, nil
}

// Language is the tag the file declares its text in.
//
// It is not checked against a registry, for the reason a device platform is
// not checked against a list: the schema holds no such set, nothing in the
// node branches on the value, and a node that refused an unregistered tag
// would refuse a reader's book until it was redeployed.
type Language string

// String renders the language.
func (l Language) String() string { return string(l) }

// IsZero reports whether the file said nothing about the language.
func (l Language) IsZero() bool { return l == "" }

// Validate reports why the language is not usable, or nil.
func (l Language) Validate() error {
	if characterCount(string(l)) > maxLanguageLength {
		return errs.New(errs.KindInvalidArgument, "the language is not usable").
			WithOp(opParseLanguage).
			WithCode(CodeInvalidLanguage).
			WithField("language", "it must be at most 20 characters long")
	}

	return nil
}

// ParseLanguage removes the surrounding space from s and validates the result.
func ParseLanguage(s string) (Language, error) {
	language := Language(strings.TrimSpace(s))
	if err := language.Validate(); err != nil {
		return "", err
	}

	return language, nil
}

// Format is the container the work is stored in, as listed in subsection 4.2.2
// of the TCC.
type Format string

// The formats UC02 admits.
const (
	FormatEPUB Format = "epub"
	FormatPDF  Format = "pdf"
	FormatMOBI Format = "mobi"
	FormatDJVU Format = "djvu"
	FormatCBZ  Format = "cbz"
)

// formats is the set above, in the order the contract enumerates it.
//
// It is a function rather than a package-level slice because the project's own
// linter forbids the second, and because a set that cannot be appended to from
// somewhere else is a set with one definition.
func formats() []Format {
	return []Format{FormatEPUB, FormatPDF, FormatMOBI, FormatDJVU, FormatCBZ}
}

// String renders the format.
func (f Format) String() string { return string(f) }

// Validate reports why the format is not usable, or nil.
//
// This is the one descriptive field that is checked against a closed set, and
// the reason is that the wire carries it as an enum: a value outside the set
// could not have come from a client of this contract, so accepting it would
// mean storing a work this node can neither name to its reader nor render.
//
// Replication is not held to this. A node that receives a row naming a format
// it does not know stores it, replicates it and returns it, exactly as the
// contract says — that path writes rows rather than calling this constructor,
// and it is what makes adding a format a non-breaking change.
func (f Format) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the format is not usable").
			WithOp(opParseFormat).
			WithCode(CodeInvalidFormat).
			WithField("format", reason)
	}

	if f == "" {
		return invalid("it must say which container the file is in")
	}

	for _, known := range formats() {
		if f == known {
			return nil
		}
	}

	return invalid("this node knows epub, pdf, mobi, djvu and cbz")
}

// ParseFormat lowercases s and validates the result.
func ParseFormat(s string) (Format, error) {
	format := Format(strings.ToLower(strings.TrimSpace(s)))
	if err := format.Validate(); err != nil {
		return "", err
	}

	return format, nil
}

// ContentHash is the digest of the file, and the name its bytes are held
// under across the whole federation.
//
// It is deliberately not a reference to something this node holds. A node
// replicating a reader without their files has every row and none of the
// bytes (D02), so the digest identifies the file rather than locating it —
// which is also what makes the same work imported by two readers converge on
// one stored object.
type ContentHash string

// String renders the digest.
func (c ContentHash) String() string { return string(c) }

// Validate reports why the digest is not usable, or nil.
func (c ContentHash) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the content digest is not usable").
			WithOp(opParseHash).
			WithCode(CodeInvalidContentHash).
			WithField("content_hash", reason)
	}

	switch {
	case string(c) == "":
		return invalid("it must name the digest of the file")
	case !isLowercaseHex(string(c), hashLength):
		return invalid("it must be a sha-256 digest, 64 lowercase hexadecimal characters")
	default:
		return nil
	}
}

// ParseContentHash lowercases s and validates the result.
//
// The lowercasing is not a courtesy. The digest is the storage key, so the
// same file described in upper case and in lower case would be two objects,
// and the deduplication the whole design rests on would silently stop working.
func ParseContentHash(s string) (ContentHash, error) {
	hash := ContentHash(strings.ToLower(strings.TrimSpace(s)))
	if err := hash.Validate(); err != nil {
		return "", err
	}

	return hash, nil
}

// Size is the length of the file in bytes, absent when the row was written by
// a client that did not declare one.
type Size int64

// Int64 renders the length.
func (s Size) Int64() int64 { return int64(s) }

// IsZero reports whether the length is absent.
func (s Size) IsZero() bool { return s == 0 }

// Validate reports why the length is not usable, or nil.
//
// It is the check library.ebooks_size_positive makes, and it admits exactly
// what that constraint admits: absent, or greater than zero. A negative length
// is the only thing refused, because zero is how absence is spelled here and a
// file of no bytes never reaches this type — the upload declares its length
// before the bytes travel, and that declaration is checked against what
// arrived.
func (s Size) Validate() error {
	if s < 0 {
		return errs.New(errs.KindInvalidArgument, "the declared length is not usable").
			WithOp(opParseSize).
			WithCode(CodeInvalidSize).
			WithField("size_bytes", "it must be greater than zero")
	}

	return nil
}

// Metadata is what a format carries and this contract does not name — series,
// ISBN, subjects (RF05).
//
// It is a map and never a scalar, because library.ebooks_metadata_is_object
// constrains the column to a JSON object. Holding it as a map is what makes
// that constraint unbreakable from here rather than checked here.
type Metadata map[string]any

// IsZero reports whether the file carried no metadata beyond the named fields.
func (m Metadata) IsZero() bool { return len(m) == 0 }

// characterCount is the length PostgreSQL measures a varchar in: characters,
// not bytes.
func characterCount(s string) int { return utf8.RuneCountInString(s) }

// hashLength is the width of a sha-256 digest written as hexadecimal, which is
// also the width library.ebooks declares for the column.
const hashLength = 64

// isLowercaseHex reports whether s is exactly length lowercase hexadecimal
// characters, which is the shape library.ebooks_hash_format enforces.
//
// It is written out rather than expressed as a regular expression because the
// project compiles none anywhere else, and a pattern would have to live in a
// package-level variable the linter forbids.
func isLowercaseHex(s string, length int) bool {
	if len(s) != length {
		return false
	}

	for _, character := range []byte(s) {
		digit := character >= '0' && character <= '9'
		letter := character >= 'a' && character <= 'f'

		if !digit && !letter {
			return false
		}
	}

	return true
}
