package smtp

import (
	"fmt"
	"mime"
	"strings"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
)

// subject is what the reader sees before they open anything.
//
// It names the action and never the credential. A subject line is the part of
// a message a notification shows on a locked screen, and a token there would be
// a token shown to whoever is holding the device.
const subject = "Recover your Quire password"

// The message is written in English, as every other string this repository
// produces is. The contract carries no locale — nothing in the protobuf says
// what language a reader reads — so a message in any single language is a
// choice, and choosing the language of the code is the one choice that does not
// have to be maintained in two places. Carrying a locale is a contract
// amendment and belongs with one.
const template = `Hello %s,

Somebody asked to reset the password of your Quire account. If it was you, use
this code to choose a new one:

    %s

It stops working at %s.

If it was not you, nothing has happened yet and you do not have to do anything:
the code above is the only way to use this request, and it reached only this
address.
`

// expiryLayout renders the deadline in a form a reader can act on: a date, a
// time and the offset it is written in.
const expiryLayout = "2 January 2006 at 15:04 MST"

// body renders what the reader reads.
func body(message service.RecoveryMessage) string {
	return fmt.Sprintf(template,
		message.DisplayName.String(),
		message.Token,
		message.ExpiresAt.UTC().Format(expiryLayout))
}

// encodeHeader renders a header value that may not be ASCII, in the encoding
// RFC 2047 defines for one.
//
// mime.QEncoding leaves a value that is already ASCII untouched, so this costs
// nothing for the subject and does the right thing the day it is translated.
func encodeHeader(value string) string { return mime.QEncoding.Encode("utf-8", value) }

// newIdentifier is the left-hand side of a Message-ID.
//
// Version 4 rather than version 7: the identifiers this node stores are
// ordered because an index reads them, and this one is read by no index. What
// it must not do is disclose when it was minted, which a version 7 identifier
// does by construction — and how many recoveries a node issues, and when, is
// not something its relay should learn.
func newIdentifier() string { return strings.ReplaceAll(uuid.NewV4().String(), "-", "") }
