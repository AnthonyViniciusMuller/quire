// Package refreshserver is the update half of UC12 that re-runs discovery
// (RF13, RF14).
//
// It is the only way a pinned key changes, and it is a deliberate act by the
// reader precisely because a node that re-pinned on its own would have no pin
// at all: an attacker able to answer for the domain would be accepted on the
// strength of answering for the domain, which is the check the pin exists to
// be (C12).
//
// It is also why a routine certificate renewal must not land here. The pin is
// over the public key and not over the certificate, so an ACME renewal keeps
// it and this call reports nothing — which is what stops an operator from
// being trained to clear the one alarm that matters.
package refreshserver

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// RefreshServer re-discovers nodes.
type RefreshServer struct {
	servers   server.Repository
	discovery service.Discovery
	clock     service.Clock
}

// RefreshServer satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RefreshServer)(nil)

// New returns the use case over its dependencies.
func New(servers server.Repository, discovery service.Discovery, clock service.Clock) *RefreshServer {
	return &RefreshServer{servers: servers, discovery: discovery, clock: clock}
}

// Execute re-discovers the node and writes back what it learned.
//
// A lookup that fails leaves the row alone. The description on record is what
// the node last said about itself, and it is worth more than nothing at all:
// a peer that is down for an afternoon must not become a peer this instance
// has forgotten how to reach.
func (r *RefreshServer) Execute(ctx context.Context, input Input) (Output, error) {
	node, err := r.servers.GetByID(ctx, input.ServerID)
	if err != nil {
		return Output{}, err
	}

	descriptor, err := r.discovery.Discover(ctx, node.Domain)
	if err != nil {
		return Output{}, err
	}

	changed, err := node.Refresh(descriptor, r.clock.Now())
	if err != nil {
		return Output{}, err
	}

	if err := r.servers.Update(ctx, node); err != nil {
		return Output{}, err
	}

	return Output{Server: node, FingerprintChanged: changed}, nil
}
