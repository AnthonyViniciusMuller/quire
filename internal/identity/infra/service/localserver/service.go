// Package localserver answers which node this is, out of the catalogue the
// federation slice owns.
//
// It is the adapter of the LocalServer port the identity use cases hold. UC14
// binds a reader to the node they registered with, so registering one needs
// the row in federation.servers that says which node this is — the identifier
// every reader's origin_server_id points at (RN08) — and the domain that forms
// the second half of their federated identifier (RN09).
//
// Phase 5 satisfied that port with a resolver of its own, which wrote the row
// from a query the identity slice carried because the federation slice did not
// exist yet. This is what replaced it: the row is written through
// server.Repository, the catalogue's own port, and the query has gone back to
// the slice that owns the table. The use cases did not change, which is what
// having declared a port was for.
package localserver

import (
	"context"
	"sync"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// Service resolves this node's row in the catalogue, once.
type Service struct {
	servers server.Repository

	// descriptor is this node as its own configuration describes it. A node
	// does not discover itself, so the values come from the environment rather
	// than from a lookup — and they are validated at construction, because a
	// base URL the catalogue would refuse is a deployment fault and belongs to
	// the start of the process rather than to the first registration.
	descriptor *server.Descriptor

	// domain is the same value in the identity slice's vocabulary. The two
	// types are separate on purpose — see server.Domain — and this is the one
	// place they meet.
	domain user.ServerDomain

	// resolved guards the cached identifier. Which node this is does not
	// change while the process runs, so the row is read once and every later
	// registration is spared the statement — but the first two registrations
	// can arrive together, and without the lock both would write it.
	resolved sync.Mutex
	id       uuid.UUID
}

// Service satisfies the port the use cases hold.
var _ service.LocalServer = (*Service)(nil)

// New returns the resolver for the node described by cfg, over the catalogue.
//
// It fails when the node's own description is not one the catalogue could
// hold. That is a misconfiguration, and the node is better stopped by it here
// than answering registrations until somebody notices.
func New(servers server.Repository, cfg *config.Server) (*Service, error) {
	base := cfg.BaseURL.String()

	descriptor := &server.Descriptor{
		Domain:  server.ParseDomain(cfg.Name),
		BaseURL: server.BaseURL(base),
		JWKSURI: server.JWKSURI(base + wellknown.JWKSPath),
		// The address peers dial, which is not the one this node listens on
		// wherever a gateway answers for it (D06). It is the same value the
		// discovery document publishes, and the catalogue holds it so that a
		// reader can read their own node out of the same list as every other.
		GRPCAuthority: server.GRPCAuthority(cfg.GRPCAdvertisedAddress),
	}

	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	return &Service{
		servers:    servers,
		descriptor: descriptor,
		domain:     user.ParseServerDomain(cfg.Name),
	}, nil
}

// Domain is the second half of every identifier this node issues.
func (s *Service) Domain() user.ServerDomain { return s.domain }

// ID is this node's row in the catalogue, created on the first call.
//
// The statement runs on whatever the context is running in, so a registration
// that opened a transaction gets the row inside it. That is deliberate: the
// row the reader is about to reference and the reader themselves then commit
// together, and a rolled back registration leaves no half-written catalogue.
func (s *Service) ID(ctx context.Context) (uuid.UUID, error) {
	s.resolved.Lock()
	defer s.resolved.Unlock()

	if s.id != (uuid.UUID{}) {
		return s.id, nil
	}

	local, err := s.servers.EnsureLocal(ctx, s.descriptor)
	if err != nil {
		return uuid.UUID{}, err
	}

	// Cached only after it committed to being this node's row. A failure
	// leaves the next call to try again, which is what an unreachable database
	// at startup deserves.
	s.id = local.ID

	return s.id, nil
}
