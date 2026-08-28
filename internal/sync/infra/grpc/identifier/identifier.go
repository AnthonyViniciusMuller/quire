// Package identifier decodes the identifiers the sync contract carries as
// strings.
//
// It is one package rather than a helper repeated in four controllers, because
// the answer to a malformed one is a decision and not a parse. Here that
// decision is the opposite of the one the other slices make: a malformed
// identifier is an invalid argument and not a not-found.
//
// The difference is who is asking. In the library and reading slices a
// malformed identifier is answered exactly as one nobody has, so that the reply
// is not an oracle for which identifiers exist. Nothing here is addressed by an
// identifier a caller could guess at: an operation names records the caller
// already holds, and it is offering them rather than asking about them. Telling
// a client that the value it sent is not a uuid costs nothing and is the only
// answer it can act on.
package identifier

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// opParse is the operation reported by this file, in the form the errs package
// expects.
const opParse = "sync/identifier: parse"

// Parse decodes an identifier a change carries, naming the field it came from.
func Parse(value, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindInvalidArgument,
			"the change names something that is not an identifier").
			WithOp(opParse).
			WithCode(operation.CodeInvalidOperation).
			WithField(field, "it must be a uuid")
	}

	return id, nil
}

// User decodes the reader a peer-facing call names.
//
// It is separate because the field is not part of a change: a device-facing
// call takes the reader from the session, and only the peer-facing one carries
// it, because a certificate identifies the calling node and not any of the
// readers it replicates.
func User(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindInvalidArgument, "the call names no reader").
			WithOp(opParse).
			WithCode(operation.CodeInvalidOperation).
			WithField("user_id", "it must be a uuid")
	}

	return id, nil
}
