// Package di builds the federation slice: it constructs every adapter, wires
// them into the use cases, wires those into the controllers, and hands back
// what the node needs from this slice.
//
// It is the only place where a concrete adapter of this slice is named.
// Everything above it holds a port, so substituting the .well-known client for
// one that reads a fixture is a change to a constructor here and to nothing
// else.
//
// It hands back two things. The gRPC surface, which the node registers. And
// the catalogue itself, because another slice needs it: UC14 binds a reader to
// the node that hosts them, so the identity slice holds a LocalServer port
// whose whole job is to resolve this instance's row in federation.servers.
//
// The direction is worth naming: the identity slice depends on a port declared
// in this slice's domain, and on nothing in its infrastructure. Wiring the two
// together is the node's job, in cmd/quired, and neither slice imports the
// other's adapters — the one exception being the authentication interceptor's
// context key, which the identity slice owns because it is the only part of
// the node that can verify a token, and which every slice's controllers read.
//
// It reads no environment variable and opens no connection. The configuration
// arrives loaded and the pool arrives open, because both are shared with the
// slices that follow and neither is this slice's to decide.
package di

import (
	"github.com/jackc/pgx/v5/pgxpool"

	addserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/addserver"
	authorizereplicausecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/authorizereplica"
	discoverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/discover"
	getserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/getserver"
	listauthorizationsusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listauthorizations"
	listserversusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listservers"
	refreshserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/refreshserver"
	removeserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/removeserver"
	revokereplicausecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/revokereplica"
	updateserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/updateserver"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/addknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/authorizereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/discoverserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/getknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/listknownservers"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/listreplicaauthorizations"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/migratehomeserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/refreshknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/removeknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/revokereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/updateknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/federationservice"
	replicarepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/replica"
	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
	clockservice "github.com/anthonyvsmuller/quire/internal/federation/infra/service/clock"
	discoveryservice "github.com/anthonyvsmuller/quire/internal/federation/infra/service/discovery"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// Container is what the node takes from this slice.
type Container struct {
	// Servers is the catalogue of nodes this instance knows. The identity
	// slice reaches it through its own port, which is what binds a reader to
	// this node.
	Servers server.Repository
	// Authorizations is which readers let which nodes hold a copy of their
	// data. The sync slice reaches it through its own port, which is what
	// refuses a peer offering changes for a reader who never allowed it (RN03,
	// RF16) — the one call in the contract refused on a reader's instruction.
	Authorizations replica.Repository
	// Service is the gRPC surface of the slice, ready to be registered.
	Service *federationservice.Service
}

// Catalogue is the node catalogue over the pool, and the one piece of this
// slice that can be built before the rest of the node.
//
// It exists because two slices need each other and neither can be second. The
// identity slice binds a reader to this node (UC14) and needs the catalogue to
// do it; this slice serves UC16, whose controller the identity slice holds
// because the work is an account, its devices and a session. Building the
// catalogue on its own breaks the knot: the identity container takes this, and
// this container takes the controller that came out of it.
//
// What it costs is nothing. The repository is a value over the pool with no
// state of its own, so the one this returns and the one Initialize builds are
// the same thing built twice — and the alternative is a container handed out
// half-built, which is a worse thing to have in a program than a constructor
// called twice.
func Catalogue(pool *pgxpool.Pool) server.Repository {
	return serverrepository.New(persist.NewManager(pool))
}

// Initialize builds the slice over the node's configuration and connection
// pool, and the controller the identity slice supplies for UC16.
//
// Nothing here can fail, which is worth contrasting with the identity slice:
// that one reads a signing key and refuses a deployment with no way to deliver
// a password recovery, and this one holds no secret and reaches no peer until
// a reader asks it to. A domain that does not answer is a failed call, not a
// node that should not have started.
func Initialize(
	cfg *config.Config, pool *pgxpool.Pool, migration *migratehomeserver.MigrateHomeServer,
) *Container {
	manager := persist.NewManager(pool)

	servers := serverrepository.New(manager)
	replicas := replicarepository.New(manager)

	discovery := discoveryservice.New(&cfg.Federation)
	clock := clockservice.New()

	// The manager itself is the unit of work: its Within is the port, so no
	// adapter stands between them.
	transaction := manager

	controllers := federationservice.Controllers{
		DiscoverServer:     discoverserver.New(discoverusecase.New(discovery)),
		AddKnownServer:     addknownserver.New(addserverusecase.New(servers, discovery, clock)),
		GetKnownServer:     getknownserver.New(getserverusecase.New(servers)),
		ListKnownServers:   listknownservers.New(listserversusecase.New(servers)),
		UpdateKnownServer:  updateknownserver.New(updateserverusecase.New(servers, replicas, transaction)),
		RefreshKnownServer: refreshknownserver.New(refreshserverusecase.New(servers, discovery, clock)),
		RemoveKnownServer:  removeknownserver.New(removeserverusecase.New(servers, replicas, transaction)),
		AuthorizeReplica: authorizereplica.New(
			authorizereplicausecase.New(servers, replicas, clock, transaction)),
		RevokeReplica: revokereplica.New(revokereplicausecase.New(replicas)),
		ListReplicaAuthorizations: listreplicaauthorizations.New(
			listauthorizationsusecase.New(servers, replicas)),
		MigrateHomeServer: migration,
	}

	return &Container{
		Servers:        servers,
		Authorizations: replicas,
		Service:        federationservice.New(&controllers),
	}
}
