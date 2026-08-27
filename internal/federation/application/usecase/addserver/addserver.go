// Package addserver is the create half of UC12: it discovers a domain and
// records what it found (RF13).
//
// The pin is established here, and that is what makes this call different from
// the lookup it contains. Peers belong to different operators and share no
// root of trust, so the public key digest read out of the discovery document
// is the anchor every later node-to-node call is checked against (RNF08, C12).
// Adopting one is a decision, which is why it happens on a call the reader
// made and never as a side effect of anything else.
package addserver

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "federation/addserver: execute"

// AddServer records nodes.
type AddServer struct {
	servers   server.Repository
	discovery service.Discovery
	clock     service.Clock
}

// AddServer satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*AddServer)(nil)

// New returns the use case over its dependencies.
func New(servers server.Repository, discovery service.Discovery, clock service.Clock) *AddServer {
	return &AddServer{servers: servers, discovery: discovery, clock: clock}
}

// Execute discovers the domain and writes the row.
//
// It looks the domain up in the catalogue before discovering it, which the
// registration of a reader deliberately does not do. The difference is what
// the lookup costs: there, a pre-check only makes the common case prettier and
// the rare case wrong, because the unique index decides either way. Here it
// saves a request to a third party — and the catalogue is node-wide, so a
// domain another reader added first is the ordinary case rather than a race.
//
// The index still decides. Two readers adding the same domain at once both
// find it free, both discover it, and one of them is answered by the
// constraint; what the pre-check removes is the outbound request, not the
// need for the constraint.
func (a *AddServer) Execute(ctx context.Context, input Input) (Output, error) {
	domain := server.ParseDomain(input.Domain)
	if err := domain.Validate(); err != nil {
		return Output{}, err
	}

	if _, err := a.servers.GetByDomain(ctx, domain); err == nil {
		return Output{}, errs.New(errs.KindAlreadyExists, "that node is already in the catalogue").
			WithOp(opExecute).
			WithCode(server.CodeDomainKnown).
			WithField("domain", "it is already known here, possibly because another reader added it")
	} else if !errors.Is(err, errs.KindNotFound) {
		return Output{}, err
	}

	descriptor, err := a.discovery.Discover(ctx, domain)
	if err != nil {
		return Output{}, err
	}

	node, err := server.New(descriptor, a.clock.Now())
	if err != nil {
		return Output{}, err
	}

	if err := a.servers.Create(ctx, node); err != nil {
		return Output{}, err
	}

	return Output{Server: node}, nil
}
