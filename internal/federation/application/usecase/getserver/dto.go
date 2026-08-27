package getserver

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// Input is a node the reader is asking about.
type Input struct {
	// ServerID is the row to read. It is a parsed identifier and not a string:
	// the controller decodes it, and one that is not a uuid never reaches
	// here.
	ServerID uuid.UUID
}

// Output is the node as the catalogue holds it.
type Output struct {
	// Server is the row, active or not. A deactivated node is still known, and
	// a reader asking about one by name is entitled to the answer.
	Server *server.Server
}
