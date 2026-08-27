package listauthorizations

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// Input is a reader asking which nodes hold a copy of their data.
type Input struct {
	// UserID is the reader, taken from the token.
	UserID uuid.UUID
	// IncludeInactive asks for the permissions that have been withdrawn as
	// well. They are hidden by default, and they are what explains a peer that
	// still holds data.
	IncludeInactive bool
}

// Authorization is one permission together with the domain of the node it
// names.
//
// The domain travels beside the identifier so that a client can name the node
// without a second call, and it is assembled here rather than joined in the
// statement: a reader's federation is the handful of nodes they chose, so the
// catalogue is one short list and pairing the two in memory costs less than a
// join costs in a repository that would then read across two entities.
type Authorization struct {
	// Replica is the decision.
	Replica *replica.Replica
	// ServerDomain is what to call the node it names.
	ServerDomain server.Domain
}

// Output is the reader's permissions.
//
// It is not paginated, for the reason the catalogue is not.
type Output struct {
	// Authorizations are ordered newest decision first.
	Authorizations []Authorization
}
