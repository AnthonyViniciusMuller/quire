// Package replicas answers which node is calling and whether a reader lets it
// hold a copy of their data, out of the catalogue the federation slice owns.
//
// It is the adapter of the Replicas port the peer-facing use case holds. Both
// questions are about federation.servers and federation.user_replicas, which
// this slice does not own, so it asks them through the federation slice's own
// repositories rather than through statements of its own — which is what keeps
// the tombstone of a revoked authorization and the meaning of an inactive node
// in the packages that have them. That is the shape
// internal/reading/infra/service/works set, and it is wired the same way, in
// cmd/quired where the containers meet.
package replicas

import (
	"context"
	"errors"
	"uuid"

	federationreplica "github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opIdentify   = "sync/replicas: identify"
	opAuthorized = "sync/replicas: authorized"
)

// The stable machine-readable codes this adapter raises.
const (
	// CodeUnknownPeer is a certificate no node in the catalogue published.
	CodeUnknownPeer = "peer_not_known"
	// CodeNotAuthorized is a node the reader has not allowed to hold a copy of
	// their data, or has stopped allowing.
	CodeNotAuthorized = "replica_not_authorized"
)

// Service answers out of the federation slice's catalogue.
type Service struct {
	catalogue      federationserver.Repository
	authorizations federationreplica.Repository
}

// Service satisfies the port the use cases hold.
var _ service.Replicas = (*Service)(nil)

// New returns the adapter over the two repositories.
func New(
	catalogue federationserver.Repository,
	authorizations federationreplica.Repository,
) *Service {
	return &Service{catalogue: catalogue, authorizations: authorizations}
}

// Identify returns the node that published the pin.
//
// The catalogue answers it through the federation slice's own port, this
// instance excluded: its own row carries its own pin, and a node that
// recognized itself as a peer would replicate to itself. A node the operator
// has stopped is refused as if it were unknown, so that a peer cannot tell
// whether it was ever in the catalogue.
func (s *Service) Identify(ctx context.Context, pin string) (uuid.UUID, error) {
	if pin == "" {
		return uuid.UUID{}, s.unknown()
	}

	node, err := s.catalogue.GetByFingerprint(ctx, federationserver.Fingerprint(pin))
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			return uuid.UUID{}, s.unknown()
		}

		return uuid.UUID{}, err
	}

	if !node.Active {
		return uuid.UUID{}, s.unknown()
	}

	return node.ID, nil
}

// Authorized reports nil when the reader lets the node hold a copy.
func (s *Service) Authorized(ctx context.Context, serverID, userID uuid.UUID) error {
	granted, err := s.authorizations.GetByPair(ctx, userID, serverID)
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			return s.refused()
		}

		return err
	}

	if !granted.Active {
		return s.refused()
	}

	return nil
}

// unknown is the answer to a certificate no active node published.
func (s *Service) unknown() error {
	return errs.New(errs.KindPermissionDenied, "this node is not replicating with you").
		WithOp(opIdentify).
		WithCode(CodeUnknownPeer)
}

// refused is the answer to a reader who has not authorized the node.
//
// It says nothing about whether the reader exists. A peer that could tell a
// reader who never authorized it from a reader who is not here would have an
// oracle for who is hosted on this node, and being able to enumerate a node's
// readers is not something an authorization for one of them should buy.
func (s *Service) refused() error {
	return errs.New(errs.KindPermissionDenied, "the reader has not authorized this node").
		WithOp(opAuthorized).
		WithCode(CodeNotAuthorized)
}
