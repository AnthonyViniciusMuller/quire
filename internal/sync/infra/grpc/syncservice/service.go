// Package syncservice registers the sync slice's gRPC service and hands each
// call to the controller that serves it.
//
// It is the whole of what the reference architecture calls the routes file. A
// gRPC service has no routing table — the generated interface is the table — so
// what remains is one forwarding method per call, and the value of keeping them
// here is that the list of what the slice serves is one file long.
//
// The Unimplemented struct is embedded because the contract requires it and
// because buf.gen.yaml says why. What that costs is that a forgotten method
// answers Unimplemented instead of failing to build, so a test calls all four
// and refuses that answer.
package syncservice

import (
	"context"

	"google.golang.org/grpc"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/pulloperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/pushoperations"
	syncstream "github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/sync"
)

// Controllers is every handler the service forwards to, which the slice's
// container fills.
type Controllers struct {
	// The two that serve UC09 one batch at a time.
	PushOperations *pushoperations.PushOperations
	PullOperations *pulloperations.PullOperations
	// The one that serves UC10 and UC11 by staying open.
	Sync *syncstream.Sync
}

// Service is the gRPC surface of the sync slice.
type Service struct {
	quirev1.UnimplementedSyncServiceServer

	controllers Controllers
}

// Service implements the generated server interface.
var _ quirev1.SyncServiceServer = (*Service)(nil)

// New returns the service over its controllers.
func New(controllers *Controllers) *Service {
	return &Service{controllers: *controllers}
}

// Register publishes the service on the node's gRPC server.
func (s *Service) Register(registrar grpc.ServiceRegistrar) {
	quirev1.RegisterSyncServiceServer(registrar, s)
}

// PushOperations stores changes a device authored (UC09, UC11).
func (s *Service) PushOperations(
	ctx context.Context, request *quirev1.PushOperationsRequest,
) (*quirev1.PushOperationsResponse, error) {
	return s.controllers.PushOperations.Handle(ctx, request)
}

// PullOperations answers with everything after the cursor (UC09, RN06).
func (s *Service) PullOperations(
	ctx context.Context, request *quirev1.PullOperationsRequest,
) (*quirev1.PullOperationsResponse, error) {
	return s.controllers.PullOperations.Handle(ctx, request)
}

// Sync is the same push and pull, kept open (UC10, UC11).
func (s *Service) Sync(stream quirev1.SyncService_SyncServer) error {
	return s.controllers.Sync.Handle(stream)
}
