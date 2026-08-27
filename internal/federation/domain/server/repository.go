package server

import (
	"context"
	"uuid"
)

// The stable machine-readable codes a repository reports.
const (
	// CodeNotFound is no such node in the catalogue.
	CodeNotFound = "server_not_found"
	// CodeServerInUse is an operation on a node some reader still allows to
	// hold a copy of their data. It is raised where a server is written and
	// not where an authorization is, because it is the server call that is
	// being refused.
	CodeServerInUse = "server_in_use"
	// CodeDomainKnown is a domain the catalogue already holds. It is not an
	// error the reader caused: the catalogue is node-wide, so another reader
	// here may have added it first, and the reply is what tells them to use
	// the row rather than to add a second one.
	CodeDomainKnown = "server_already_known"
)

// Repository is the port through which the use cases of the federation slice
// read and write the catalogue. It belongs to the domain; what satisfies it
// lives in internal/federation/infra/repository/server.
//
// As in every other slice, the context is passed so that a call can join the
// transaction the manager carries, and a node that does not exist is an error
// of kind errs.KindNotFound rather than a zero value.
type Repository interface {
	// EnsureLocal creates or refreshes the row that says which node this is,
	// and returns it.
	//
	// It is an upsert on the domain rather than an insert, because it runs on
	// every start: the identifier the row already has is referenced by every
	// reader hosted here, so what a redeployment may have changed — the base
	// URL, the signing key location — is rewritten while that identifier is
	// left alone.
	//
	// It is the only way a local row is written. The identity slice reaches it
	// through its own LocalServer port, which is what UC14 binds a reader
	// with.
	EnsureLocal(ctx context.Context, descriptor *Descriptor) (*Server, error)

	// Create records a peer, and reports errs.KindAlreadyExists with
	// CodeDomainKnown when the catalogue already holds its domain.
	Create(ctx context.Context, node *Server) error

	// Update writes back what a refresh learned and whether the node takes
	// part. Neither the domain nor the local flag is writable: the first is
	// what identifies the row, and the second is what the partial unique index
	// allows exactly one of.
	Update(ctx context.Context, node *Server) error

	// Delete forgets the peer, and reports whether the row went.
	//
	// It refuses this instance, and a node any reader still authorizes, in the
	// statement itself rather than beside it: a check the caller ran a moment
	// earlier is a check something could have invalidated since, and the
	// foreign key cascades — a delete that got past the check would take that
	// reader's authorization with it rather than being refused.
	//
	// A false is therefore not an error but a refusal, and the caller is the
	// one that has read enough to say which of the two refused it.
	Delete(ctx context.Context, id uuid.UUID) (bool, error)

	// GetByID reads a node by primary key, active or not.
	GetByID(ctx context.Context, id uuid.UUID) (*Server, error)

	// GetByDomain reads a node by the authority it is known as, which is the
	// value UC12 addresses a lookup to and the one the catalogue makes unique.
	GetByDomain(ctx context.Context, domain Domain) (*Server, error)

	// List reads the catalogue, ordered by domain so that it does not
	// reshuffle between two calls. Deactivated nodes are included only when
	// asked for.
	List(ctx context.Context, includeInactive bool) ([]*Server, error)
}
