// Package discover is UC13: it resolves a domain to the services the node at
// it exposes, and stores nothing (RF14).
//
// It is the lookup the rest of the federation is built on, and the reason it
// is a use case of its own rather than a step inside adding a node is that a
// reader does it before they have decided anything. UC14 includes it — an
// application choosing where to register asks this first — and so does the
// first half of UC12, which discovers a domain and then records what it found.
//
// Storing nothing is the whole of its contract. A lookup that wrote a row
// would make reading about a node indistinguishable from adopting it, and the
// pin a document carries would then be adopted by a reader who was only
// looking.
package discover

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// Discover resolves domains.
type Discover struct {
	discovery service.Discovery
}

// Discover satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*Discover)(nil)

// New returns the use case over its dependencies.
func New(discovery service.Discovery) *Discover {
	return &Discover{discovery: discovery}
}

// Execute asks the domain what it publishes about itself.
//
// The domain is validated here rather than in the adapter alone, so that
// something which is not a host is refused before a request is built out of
// it: the value goes into the authority of a URL, and a value that needs
// escaping there is a value that could address the lookup somewhere else.
func (d *Discover) Execute(ctx context.Context, input Input) (Output, error) {
	domain := server.ParseDomain(input.Domain)
	if err := domain.Validate(); err != nil {
		return Output{}, err
	}

	descriptor, err := d.discovery.Discover(ctx, domain)
	if err != nil {
		return Output{}, err
	}

	return Output{Descriptor: descriptor}, nil
}
