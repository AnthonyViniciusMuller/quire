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

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opServer is the operation reported by this file, in the form the errs
// package expects.
const opServer = "federation/identifier: server"

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
