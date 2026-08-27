package discover

import "github.com/anthonyvsmuller/quire/internal/federation/domain/server"

// Input is a domain a reader wants to know about.
//
// The field is a string and not a value object: this is the edge, and what
// arrives here has been through nothing but a protobuf decoder. Turning it
// into one is the first thing Execute does, and it is where something that is
// not a host is rejected with the name of the field it came from.
type Input struct {
	// Domain is the authority to address the lookup to, as the reader typed
	// it. It is folded and trimmed before anything is done with it, so that
	// Quire-A.Example reaches the node quire-a.example does.
	Domain string
}

// Output is what the node at that domain publishes about itself.
type Output struct {
	// Descriptor is the answer as it was read, and nothing was stored. What
	// the answer is worth keeping is the next call's decision, which is what
	// tells this lookup from the first half of adding a node to the catalogue.
	Descriptor *server.Descriptor
}
