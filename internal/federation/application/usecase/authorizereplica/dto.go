package authorizereplica

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
)

// Input is a reader allowing one node to hold a copy of their data.
type Input struct {
	// UserID is the reader the permission belongs to, taken from the token and
	// never from the request: a permission somebody else could grant would not
	// be RN03.
	UserID uuid.UUID
	// ServerID is the node the copy may live on. It has to be in the
	// catalogue, which is what makes the pin the reader is trusting one this
	// instance actually learned.
	ServerID uuid.UUID
	// ReplicatesFiles is whether the permission covers the e-book files as
	// well as the metadata. Metadata without the files is the cheap replica a
	// reader is most likely to want on a node they do not own.
	ReplicatesFiles bool
}

// Output is the permission as it now stands.
type Output struct {
	// Authorization is the row that was written, which is the same row as any
	// earlier decision about this pair.
	Authorization *replica.Replica
}
