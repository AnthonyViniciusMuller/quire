// Package localserver answers which node this is, from the catalogue in
// federation.servers.
//
// It is the temporary adapter of the port the identity use cases hold. The
// catalogue belongs to the federation slice, which lands in phase 6; until it
// does, UC14 still has to bind a reader to a row that exists, and this creates
// that one row from the node's own configuration.
package localserver

import (
	"context"
	"sync"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// opID is the operation reported by this file, in the form the errs package
// expects.
const opID = "identity/localserver: id"

// Service resolves this node's row in the catalogue, once.
type Service struct {
	manager *persist.Manager

	domain  user.ServerDomain
	baseURL string
	jwksURI string

	// resolved guards the cached identifier. Which node this is does not change
	// while the process runs, so the row is read once and every later
	// registration is spared the statement — but the first two registrations
	// can arrive together, and without the lock both would write it.
	resolved sync.Mutex
	id       uuid.UUID
}

// Service satisfies the port the use cases hold.
var _ service.LocalServer = (*Service)(nil)

// New returns the resolver for the node described by server.
func New(manager *persist.Manager, server *config.Server) *Service {
	base := server.BaseURL.String()

	return &Service{
		manager: manager,
		domain:  user.ServerDomain(server.Name),
		baseURL: base,
		jwksURI: base + wellknown.JWKSPath,
	}
}

// Domain is the second half of every identifier this node issues.
func (s *Service) Domain() user.ServerDomain { return s.domain }

// ID is this node's row in the catalogue, created on the first call.
//
// The statement runs on whatever the context is running in, so a registration
// that opened a transaction gets the row inside it. That is deliberate: the row
// the reader is about to reference and the reader themselves then commit
// together, and a rolled back registration leaves no half-written catalogue.
func (s *Service) ID(ctx context.Context) (uuid.UUID, error) {
	s.resolved.Lock()
	defer s.resolved.Unlock()

	if s.id != (uuid.UUID{}) {
		return s.id, nil
	}

	id, err := identitydb.New(s.manager.Executor(ctx)).EnsureLocalServer(ctx, identitydb.EnsureLocalServerParams{
		Domain:  s.domain.String(),
		BaseUrl: s.baseURL,
		JwksUri: &s.jwksURI,
	})
	if err != nil {
		return uuid.UUID{}, persist.Classify(err, opID)
	}

	// Cached only after it committed to being this node's row. A failure leaves
	// the next call to try again, which is what an unreachable database at
	// startup deserves.
	s.id = id

	return id, nil
}
