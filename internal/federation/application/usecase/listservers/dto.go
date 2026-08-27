package listservers

import "github.com/anthonyvsmuller/quire/internal/federation/domain/server"

// Input is a reader asking what the node knows.
type Input struct {
	// IncludeInactive asks for the nodes that have been deactivated as well.
	// They are hidden by default and readable on request: they are still
	// known, and what they are not is replicated to.
	IncludeInactive bool
}

// Output is the catalogue.
//
// It is not paginated, and the contract says why: a reader's federation is the
// handful of nodes they chose, and a page token here would be a parameter no
// client ever sets.
type Output struct {
	// Servers are ordered by domain, which is unique, so the list does not
	// reshuffle between two calls. This node's own row is among them, marked,
	// because a reader points at their origin server whether it is local or
	// remote.
	Servers []*server.Server
}
