// Package identifier decodes the identifiers the federation contract carries
// as strings.
//
// It is one package rather than a helper repeated in six controllers, because
// the answer to a malformed one is a decision and not a parse: a value that is
// not a uuid is answered exactly as one nobody has. The reply must not be an
// oracle for which identifiers exist, and a client that sent a broken one
// learns the same thing either way — that there is nothing there.
package identifier

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opServer is the operation reported by this file, in the form the errs
// package expects.
const (
	opServer = "federation/identifier: server"
	opReader = "federation/identifier: reader"
	opDevice = "federation/identifier: device"
)

// Server decodes the identifier of a node in the catalogue.
func Server(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindNotFound, "no such node in the catalogue").
			WithOp(opServer).
			WithCode(server.CodeNotFound)
	}

	return id, nil
}

// User parses the identifier of a reader a peer names.
//
// A peer that names a reader it cannot spell has made a malformed call, not a
// call about a reader who is not here — which is why this is an invalid
// argument where [Server] is a not found: a device asking after a node it
// mistyped and a node claiming a reader it cannot name are different
// mistakes.
func User(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindInvalidArgument, "the call names no reader").
			WithOp(opReader).
			WithCode(replica.CodeInvalidAuthorization).
			WithField("user_id", "it must be a uuid")
	}

	return id, nil
}

// Device parses the identifier of a device a peer names.
func Device(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.Wrap(err, errs.KindInvalidArgument, "the call names a device it cannot spell").
			WithOp(opDevice).
			WithCode(replica.CodeInvalidAuthorization).
			WithField("device_id", "it must be a uuid")
	}

	return id, nil
}
