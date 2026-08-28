// Package federationservice registers the federation slice's gRPC service and
// hands each call to the controller that serves it.
//
// It is the whole of what the reference architecture calls the routes file. A
// gRPC service has no routing table — the generated interface is the table —
// so what remains is one forwarding method per call, and the value of keeping
// them here is that the list of what the slice serves is one file long.
//
// The Unimplemented struct is embedded because the contract requires it and
// because buf.gen.yaml says why: this contract grows an RPC in almost every
// phase, and a handler that did not compile until it implemented one would
// make every such phase start with unrelated work. What that costs is that a
// forgotten method answers Unimplemented instead of failing to build, so a
// test calls all eleven and refuses that answer.
//
// MigrateHomeServer is the eleventh, and it is the one whose controller belongs
// to another slice. What UC16 changes is which node a reader belongs to, which
// is a fact about the federation; what it writes is an account, its devices and
// a session, which the identity slice is the only holder of. So the method is
// served here and the work is done there, and the container is where the two
// meet.
package federationservice

import (
	"context"

	"google.golang.org/grpc"

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
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// Controllers is every handler the service forwards to, which the slice's
// container fills.
type Controllers struct {
	// DiscoverServer serves UC13.
	DiscoverServer *discoverserver.DiscoverServer
	// The six that serve UC12.
	AddKnownServer     *addknownserver.AddKnownServer
	GetKnownServer     *getknownserver.GetKnownServer
	ListKnownServers   *listknownservers.ListKnownServers
	UpdateKnownServer  *updateknownserver.UpdateKnownServer
	RefreshKnownServer *refreshknownserver.RefreshKnownServer
	RemoveKnownServer  *removeknownserver.RemoveKnownServer
	// The three that serve UC15.
	AuthorizeReplica          *authorizereplica.AuthorizeReplica
	RevokeReplica             *revokereplica.RevokeReplica
	ListReplicaAuthorizations *listreplicaauthorizations.ListReplicaAuthorizations
	// The one that serves UC16, whose use case is the identity slice's.
	MigrateHomeServer *migratehomeserver.MigrateHomeServer
}

// Service is the gRPC surface of the federation slice.
type Service struct {
	quirev1.UnimplementedFederationServiceServer

	controllers Controllers
}

// Service implements the generated server interface.
var _ quirev1.FederationServiceServer = (*Service)(nil)

// New returns the service over its controllers.
func New(controllers *Controllers) *Service {
	return &Service{controllers: *controllers}
}

// Register publishes the service on the node's gRPC server.
func (s *Service) Register(registrar grpc.ServiceRegistrar) {
	quirev1.RegisterFederationServiceServer(registrar, s)
}

// DiscoverServer resolves a domain to the services it exposes (UC13).
func (s *Service) DiscoverServer(
	ctx context.Context, request *quirev1.DiscoverServerRequest,
) (*quirev1.DiscoverServerResponse, error) {
	return s.controllers.DiscoverServer.Handle(ctx, request)
}

// AddKnownServer discovers a domain and records what it found (UC12).
func (s *Service) AddKnownServer(
	ctx context.Context, request *quirev1.AddKnownServerRequest,
) (*quirev1.AddKnownServerResponse, error) {
	return s.controllers.AddKnownServer.Handle(ctx, request)
}

// GetKnownServer answers with one node in the catalogue (UC12).
func (s *Service) GetKnownServer(
	ctx context.Context, request *quirev1.GetKnownServerRequest,
) (*quirev1.GetKnownServerResponse, error) {
	return s.controllers.GetKnownServer.Handle(ctx, request)
}

// ListKnownServers answers with the catalogue (UC12).
func (s *Service) ListKnownServers(
	ctx context.Context, request *quirev1.ListKnownServersRequest,
) (*quirev1.ListKnownServersResponse, error) {
	return s.controllers.ListKnownServers.Handle(ctx, request)
}

// UpdateKnownServer writes whether a node takes part in replication (UC12).
func (s *Service) UpdateKnownServer(
	ctx context.Context, request *quirev1.UpdateKnownServerRequest,
) (*quirev1.UpdateKnownServerResponse, error) {
	return s.controllers.UpdateKnownServer.Handle(ctx, request)
}

// RefreshKnownServer re-runs discovery against a node (UC12, RF14).
func (s *Service) RefreshKnownServer(
	ctx context.Context, request *quirev1.RefreshKnownServerRequest,
) (*quirev1.RefreshKnownServerResponse, error) {
	return s.controllers.RefreshKnownServer.Handle(ctx, request)
}

// RemoveKnownServer forgets a node (UC12).
func (s *Service) RemoveKnownServer(
	ctx context.Context, request *quirev1.RemoveKnownServerRequest,
) (*quirev1.RemoveKnownServerResponse, error) {
	return s.controllers.RemoveKnownServer.Handle(ctx, request)
}

// AuthorizeReplica allows a known node to hold a copy of the reader's data
// (UC15, RF16).
func (s *Service) AuthorizeReplica(
	ctx context.Context, request *quirev1.AuthorizeReplicaRequest,
) (*quirev1.AuthorizeReplicaResponse, error) {
	return s.controllers.AuthorizeReplica.Handle(ctx, request)
}

// RevokeReplica withdraws that permission (UC15, RF16).
func (s *Service) RevokeReplica(
	ctx context.Context, request *quirev1.RevokeReplicaRequest,
) (*quirev1.RevokeReplicaResponse, error) {
	return s.controllers.RevokeReplica.Handle(ctx, request)
}

// ListReplicaAuthorizations answers with which nodes hold a copy (UC15).
func (s *Service) ListReplicaAuthorizations(
	ctx context.Context, request *quirev1.ListReplicaAuthorizationsRequest,
) (*quirev1.ListReplicaAuthorizationsResponse, error) {
	return s.controllers.ListReplicaAuthorizations.Handle(ctx, request)
}

// MigrateHomeServer takes a reader in from another origin server (UC16, RF17).
func (s *Service) MigrateHomeServer(
	ctx context.Context, request *quirev1.MigrateHomeServerRequest,
) (*quirev1.MigrateHomeServerResponse, error) {
	return s.controllers.MigrateHomeServer.Handle(ctx, request)
}
