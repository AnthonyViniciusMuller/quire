package authorizereplica

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
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
	// ServerDomain is what to call the node it names.
	//
	// The reply carries a domain beside the identifier, and this use case has
	// already read the catalogue row in order to check it — assembling it in
	// the controller would have been the controller deciding something, and
	// reading it there would have been a second statement for a value already
	// in hand.
	ServerDomain server.Domain
}
