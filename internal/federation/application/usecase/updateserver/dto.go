package updateserver

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// Input is the one field of a catalogue row a reader may write.
type Input struct {
	// ServerID is the row to write.
	ServerID uuid.UUID
	// Active is whether the node takes part in replication. Everything else in
	// the record was learned from the node itself and is refreshed rather than
	// typed, which is why this is the whole of the update.
	Active bool
}

// Output is the node as the catalogue now holds it.
type Output struct {
	// Server is the row that was written.
	Server *server.Server
}
