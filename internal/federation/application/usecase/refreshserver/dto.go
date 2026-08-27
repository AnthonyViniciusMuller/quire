package refreshserver

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// Input is a node the reader is re-discovering.
type Input struct {
	// ServerID is the row to refresh. The lookup goes to the domain on record
	// and not to one the caller supplies, which is what makes this a refresh
	// of that node rather than a way to point the row at another.
	ServerID uuid.UUID
}

// Output is the node as the catalogue now holds it, and what changed about it.
type Output struct {
	// Server is the row that was written.
	Server *server.Server
	// FingerprintChanged is true when the node now presents a different public
	// key from the one on record.
	//
	// It is reported rather than withheld. The new pin is stored — there is
	// nothing here to check it against, and a record holding the old one could
	// not be used against the node as it is now — and the reader is told,
	// because a deliberate key rotation and an interception look identical
	// from this side and they are the only party who can tell them apart.
	FingerprintChanged bool
}
