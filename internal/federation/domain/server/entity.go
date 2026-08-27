// Package server is the catalogue of nodes this instance knows: the entity,
// the value objects that describe one, and the port a repository has to
// satisfy.
//
// The catalogue is what makes the federation addressable. A reader types a
// domain, discovery over RFC 8615 turns it into an origin, a signing key
// location and a pin, and a row here is where that answer is kept so that the
// lookup is done once rather than on every call (UC12, UC13; RF13, RF14).
//
// Two properties of the table shape everything below.
//
// The catalogue is node-wide, not per-reader. federation.servers names no
// user, and it is federation.user_replicas that carries the reader — knowing
// that a node exists is shared, and permission for it to hold a copy is not.
// RN03 lives in the second table, which is why nothing here asks who is
// calling. UC12 reads as though the catalogue were the reader's own, and C15
// in docs/tcc-corrections.md is why the schema is the half that is right: a
// row per reader would be a pinned key per reader, and the wrong one would be
// invisible against the others.
//
// One row is this instance. Every reader hosted here points at it through
// identity.users.origin_server_id, so it is the row that must exist before the
// first registration and the row that must never be removed. A partial unique
// index allows exactly one, because "is this reader local" decides whether the
// node authenticates them or merely replicates them, and two rows would make
// that question unanswerable.
package server

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew     = "federation/server: new"
	opRefresh = "federation/server: refresh"
	opLocal   = "federation/server: local"
)

// The stable machine-readable codes the entity raises.
const (
	// CodeInvalidServer is a node that could not be recorded for a reason none
	// of the value objects owns.
	CodeInvalidServer = "invalid_server"
	// CodeDomainMismatch is a refresh answered by a different domain from the
	// one on record.
	CodeDomainMismatch = "server_domain_mismatch"
	// CodeLocalServer is an attempt to edit or forget this node's own row.
	CodeLocalServer = "local_server"
)

// Props is everything about a node other than its identifier.
type Props struct {
	// Descriptor is what the node published about itself. Every field of it is
	// learned from the node and refreshed, never typed by a reader.
	Descriptor

	// IsLocal marks the one row that is this instance.
	IsLocal bool

	// DiscoveredAt is when the description above was last learned, and the
	// zero instant on a row nothing has discovered yet.
	DiscoveredAt time.Time

	// Active is whether the node takes part in replication. Clearing it stops
	// the traffic without losing what discovery already learned, which is what
	// makes it different from forgetting the node.
	Active bool
}

// Server is one node in the catalogue (MER: servidor; federation.servers).
type Server struct {
	// ID is what every reader hosted here and every replica authorization
	// references. It outlives a redeployment: a node whose base URL changed is
	// the same node.
	ID uuid.UUID

	Props
}

// New records a peer this node has just discovered.
//
// It is never this instance. The local row is written by the repository's
// EnsureLocal, from the node's own configuration rather than from a lookup:
// a node does not discover itself, and the row has to exist before anything
// could ask it to.
func New(descriptor Descriptor, discoveredAt time.Time) (*Server, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	if discoveredAt.IsZero() {
		return nil, errs.New(errs.KindInvalidArgument, "the node was recorded without a discovery time").
			WithOp(opNew).
			WithCode(CodeInvalidServer).
			WithField("discovered_at", "it must say when the description was learned")
	}

	return &Server{
		// Minted here rather than by the column default, for the reason a
		// reader's is: the caller holds the identifier before the insert, and
		// the reply that reports the node has to carry it.
		ID: uuid.New(),
		Props: Props{
			Descriptor:   descriptor,
			DiscoveredAt: discoveredAt,
			Active:       true,
		},
	}, nil
}

// Restore rebuilds a node already stored, without minting an identifier: the
// id is the one every reader hosted here references, and a repository that
// replaced it would orphan all of them.
func Restore(id uuid.UUID, props *Props) *Server {
	return &Server{ID: id, Props: *props}
}

// Refresh replaces what discovery learned and reports whether the pin moved.
//
// The new pin is applied, not withheld. Withholding it would leave the reader
// with a record that cannot be used against the node as it is now, and there
// is nothing this node could check the new value against anyway. What the
// reader gets instead is the fact: a rotation and an interception look
// identical from here, and they are the only party that can tell them apart —
// which is also why this happens on a call they made rather than on a
// schedule (C12).
func (s *Server) Refresh(descriptor Descriptor, at time.Time) (bool, error) {
	if err := descriptor.Validate(); err != nil {
		return false, err
	}

	// The lookup is addressed to the domain on record, so an answer under a
	// different one is not a refresh of this row. Accepting it would move a
	// node's identity without the reader ever naming the new one.
	if descriptor.Domain != s.Domain {
		return false, errs.New(errs.KindFailedPrecondition, "that description belongs to another node").
			WithOp(opRefresh).
			WithCode(CodeDomainMismatch).
			WithField("domain", "the record names "+s.Domain.String()+
				" and the description names "+descriptor.Domain.String())
	}

	changed := s.CertificateFingerprint != descriptor.CertificateFingerprint

	s.Descriptor = descriptor
	s.DiscoveredAt = at

	return changed, nil
}

// SetActive says whether the node takes part in replication. It is the only
// field of a catalogue row a reader may write, because everything else in it
// was learned from the node itself.
func (s *Server) SetActive(active bool) error {
	if s.IsLocal {
		return s.refuseLocal("deactivating this node would stop it replicating on its own behalf")
	}

	s.Active = active

	return nil
}

// Removable reports why the node may not be forgotten, or nil.
//
// This instance is never removable. Every reader hosted here references the
// row, so removing it is a foreign key violation at best and, if it succeeded,
// a node that no longer knows who it is.
func (s *Server) Removable() error {
	if s.IsLocal {
		return s.refuseLocal("every reader hosted here references it")
	}

	return nil
}

// Pinned reports whether the node published a certificate pin, which is what
// node-to-node mTLS is checked against (RNF08). A peer without one can be
// recorded and cannot be replicated to outside development.
func (s *Server) Pinned() bool { return !s.CertificateFingerprint.IsZero() }

// refuseLocal is the answer to any attempt to edit or forget this instance.
func (s *Server) refuseLocal(reason string) error {
	return errs.New(errs.KindFailedPrecondition, "that node is this one").
		WithOp(opLocal).
		WithCode(CodeLocalServer).
		WithField("server_id", reason)
}
