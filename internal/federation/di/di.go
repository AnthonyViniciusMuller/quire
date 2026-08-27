// Package di builds the federation slice: it constructs every adapter, wires
// them into the use cases, and hands back what the node needs from this slice.
//
// It is the only place where a concrete adapter of this slice is named.
//
// What it hands back today is the catalogue itself, because another slice
// needs it: UC14 binds a reader to the node that hosts them, so the identity
// slice holds a LocalServer port whose whole job is to resolve this instance's
// row in federation.servers. That row belongs here, so the adapter that
// satisfies the port is built from this container rather than from a temporary
// resolver of its own — which is what phase 5 had and what this replaces.
//
// The direction is worth naming: the identity slice depends on a port declared
// in this slice's domain, and on nothing in its infrastructure. Wiring the two
// together is the node's job, in cmd/quired, and neither slice imports the
// other's adapters.
package di

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// Container is what the node takes from this slice.
type Container struct {
	// Servers is the catalogue of nodes this instance knows. The identity
	// slice reaches it through its own port, which is what binds a reader to
	// this node.
	Servers server.Repository
}

// Initialize builds the slice over the node's connection pool.
//
// It opens no connection and reads no environment variable: the pool arrives
// open because it is shared with every other slice, and the configuration
// arrives loaded because neither is this slice's to decide.
func Initialize(pool *pgxpool.Pool) *Container {
	manager := persist.NewManager(pool)

	return &Container{Servers: serverrepository.New(manager)}
}
