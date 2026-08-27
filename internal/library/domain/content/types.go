package content

import (
	"strings"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseMediaType = "library/content: parse media type"
	opParseLocator   = "library/content: parse locator"
)

// The stable machine-readable codes this package attaches to the errors it
// raises.
const (
	// CodeInvalidMediaType is a media type this node will not record.
	CodeInvalidMediaType = "invalid_media_type"
	// CodeInvalidLocator is a bucket or a key the column cannot hold.
	CodeInvalidLocator = "invalid_storage_locator"
)

// The widths library.ebook_contents declares.
const (
	maxMediaTypeLength = 100
	maxBucketLength    = 255
	maxKeyLength       = 512
)

// MediaType is what the bytes are, as the client declared them.
type MediaType string

// String renders the media type.
func (m MediaType) String() string { return string(m) }

// Validate reports why the media type is not usable, or nil.
//
// The shape is checked and the value is not looked up in a registry, for the
// reason a language tag is not: nothing in the node branches on it, it is
// handed back to the client that stored it, and a node that refused an
// unregistered type would refuse a reader's file until it was redeployed.
//
// The shape is checked because the value is returned in a header by the
// download stream, and a media type with a space or a newline in it is a value
// that would have to be escaped somewhere or would corrupt something.
func (m MediaType) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the media type is not usable").
			WithOp(opParseMediaType).
			WithCode(CodeInvalidMediaType).
			WithField("media_type", reason)
	}

	value := string(m)

	switch {
	case value == "":
		return invalid("it must say what the bytes are")
	case characterCount(value) > maxMediaTypeLength:
		return invalid("it must be at most 100 characters long")
	case strings.Count(value, "/") != 1 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/"):
		return invalid("it must be a media type, of the form type/subtype")
	case strings.ContainsAny(value, " \t\r\n"):
		return invalid("it must not contain white space")
	default:
		return nil
	}
}

// ParseMediaType lowercases s and validates the result. Media types are
// case-insensitive, and storing one form means a value read back compares
// equal to the one that was stored.
func ParseMediaType(s string) (MediaType, error) {
	mediaType := MediaType(strings.ToLower(strings.TrimSpace(s)))
	if err := mediaType.Validate(); err != nil {
		return "", err
	}

	return mediaType, nil
}

// Locator is where the object store put the bytes.
//
// The bucket travels with the key, as library.ebook_contents records it,
// so that a node can be pointed at a different bucket without rewriting the
// rows that already exist — and so that a node moved from one provider to
// another can still read what it stored under the old one.
type Locator struct {
	// Bucket is the container the object lives in.
	Bucket string
	// Key is the name it lives under.
	Key string
}

// IsZero reports whether nothing has been stored.
func (l Locator) IsZero() bool { return l.Bucket == "" && l.Key == "" }

// Validate reports why the locator is not usable, or nil.
func (l Locator) Validate() error {
	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the storage location is not usable").
			WithOp(opParseLocator).
			WithCode(CodeInvalidLocator).
			WithField(field, reason)
	}

	switch {
	case l.Bucket == "":
		return invalid("storage_bucket", "it must name the container the object lives in")
	case characterCount(l.Bucket) > maxBucketLength:
		return invalid("storage_bucket", "it must be at most 255 characters long")
	case l.Key == "":
		return invalid("storage_key", "it must name the object")
	case characterCount(l.Key) > maxKeyLength:
		return invalid("storage_key", "it must be at most 512 characters long")
	default:
		return nil
	}
}

// characterCount is the length PostgreSQL measures a varchar in: characters,
// not bytes.
func characterCount(s string) int { return utf8.RuneCountInString(s) }
