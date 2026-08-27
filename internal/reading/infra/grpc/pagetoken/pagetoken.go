// Package pagetoken encodes the cursor a page of marks continues from, and
// decodes it back.
//
// The contract carries an opaque string and the domain carries a keyset, so
// something has to translate between them, and translation is what a controller
// does. It lives in one package because two controllers would otherwise agree
// on a format by coincidence.
//
// The keyset here is the identifier alone, which is the whole of the ordering
// as well: an annotation has no immutable value to sort by, so a page ordered
// by anything else would skip or repeat a mark edited between two requests. The
// library slice's token carries a pair, because a work has an import instant
// that never changes, and the two formats are deliberately not one — a token
// issued by one service and sent to the other is refused rather than misread.
//
// The token is opaque and deliberately not a secret. It encodes the position
// and nothing else: a client that decoded one would learn the identifier of a
// mark it has already been shown. What the encoding buys is that the format can
// change without a client that stored a token being able to tell, and that a
// token from an older format is refused rather than misread, because the check
// below is on the shape and not on a version number.
package pagetoken

import (
	"encoding/base64"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opDecode is the operation reported by this file, in the form the errs package
// expects.
const opDecode = "reading/pagetoken: decode"

// CodeInvalidPageToken is a token this node did not issue.
const CodeInvalidPageToken = "invalid_page_token"

// Encode renders a cursor as the token a client sends back.
func Encode(cursor annotation.Cursor) string {
	if cursor.IsZero() {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString([]byte(cursor.ID.String()))
}

// Decode reads a token back into a cursor, and answers the zero value for an
// empty one — which is a client asking for the first page.
func Decode(token string) (annotation.Cursor, error) {
	if token == "" {
		return annotation.Cursor{}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return annotation.Cursor{}, invalid(err)
	}

	id, err := uuid.Parse(string(decoded))
	if err != nil {
		return annotation.Cursor{}, invalid(err)
	}

	return annotation.Cursor{ID: id}, nil
}

// invalid is the answer to a token this node did not issue.
//
// It is an invalid argument and not a not-found, because unlike an identifier a
// token names no entity: there is nothing for a refusal to reveal the existence
// of, and a client that sent a corrupted one needs to be told rather than served
// an empty page it would read as the end of the list.
func invalid(cause error) error {
	return errs.Wrap(cause, errs.KindInvalidArgument, "that page token is not one this node issued").
		WithOp(opDecode).
		WithCode(CodeInvalidPageToken).
		WithField("page_token", "send the value from the previous reply, or nothing for the first page")
}
