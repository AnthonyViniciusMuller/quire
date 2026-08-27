// Package pagetoken encodes the cursor a page of works continues from, and
// decodes it back.
//
// The contract carries an opaque string and the domain carries a keyset — an
// import instant and an identifier — so something has to translate between
// them, and translation is what a controller does. It lives in one package
// because two controllers would otherwise agree on a format by coincidence.
//
// The token is deliberately opaque and deliberately not a secret. It encodes
// the position and nothing else: a client that decoded one would learn when a
// work it has already been shown was imported, which it was already shown.
// What the encoding buys is that the format can change without a client that
// stored a token being able to tell — and a token from an older format is
// refused rather than misread, because the check below is on the shape and not
// on a version number.
package pagetoken

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opDecode is the operation reported by this file, in the form the errs
// package expects.
const opDecode = "library/pagetoken: decode"

// CodeInvalidPageToken is a token this node did not issue.
const CodeInvalidPageToken = "invalid_page_token"

// separator divides the two halves of the encoded cursor. A colon cannot occur
// in either — one is a decimal integer and the other is a uuid — so the split
// is unambiguous without escaping.
const separator = ":"

// Encode renders a cursor as the token a client sends back.
//
// The instant travels as microseconds since the epoch, which is the resolution
// the column keeps: a token carrying nanoseconds would name a position between
// two rows, and the row comparison would then skip or repeat one.
func Encode(cursor ebook.Cursor) string {
	if cursor.IsZero() {
		return ""
	}

	position := strconv.FormatInt(cursor.ImportedAt.UnixMicro(), 10) + separator + cursor.ID.String()

	return base64.RawURLEncoding.EncodeToString([]byte(position))
}

// Decode reads a token back into a cursor, and answers the zero value for an
// empty one — which is a client asking for the first page.
func Decode(token string) (ebook.Cursor, error) {
	if token == "" {
		return ebook.Cursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ebook.Cursor{}, invalid(err)
	}

	position, id, found := strings.Cut(string(decoded), separator)
	if !found {
		return ebook.Cursor{}, invalid(nil)
	}

	micros, err := strconv.ParseInt(position, 10, 64)
	if err != nil {
		return ebook.Cursor{}, invalid(err)
	}

	parsed, err := uuid.Parse(id)
	if err != nil {
		return ebook.Cursor{}, invalid(err)
	}

	return ebook.Cursor{
		ImportedAt: time.UnixMicro(micros).UTC().Truncate(crdt.Resolution),
		ID:         parsed,
	}, nil
}

// invalid is the answer to a token this node did not issue.
//
// It is an invalid argument and not a not-found, because unlike an identifier
// a token names no entity: there is nothing for a refusal to reveal the
// existence of, and a client that sent a corrupted one needs to be told rather
// than served an empty page it would read as the end of the collection.
func invalid(cause error) error {
	return errs.Wrap(cause, errs.KindInvalidArgument, "that page token is not one this node issued").
		WithOp(opDecode).
		WithCode(CodeInvalidPageToken).
		WithField("page_token", "send the value from the previous reply, or nothing for the first page")
}
