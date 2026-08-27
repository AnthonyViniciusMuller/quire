package service

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// LocalServer is this node's own identity inside the federation catalogue.
//
// UC14 binds a reader to the node they registered with, so registering one
// needs the row in federation.servers that says which node this is — the
// identifier every reader's origin_server_id points at (RN08) — and the domain
// that forms the second half of their federated identifier (RN09).
//
// It is a port because the catalogue belongs to the federation slice, which
// arrives later: the use cases here name what they need, and phase 6 replaces
// what satisfies it without any of them changing.
type LocalServer interface {
	// ID is this node's row in the catalogue, created if it is not there yet.
	//
	// It takes a context because the first call reaches the database. Later
	// ones do not: which node this is does not change while the process runs.
	ID(ctx context.Context) (uuid.UUID, error)

	// Domain is the second half of every identifier this node issues. It is
	// configuration, so it needs neither a context nor an error.
	Domain() user.ServerDomain
}
