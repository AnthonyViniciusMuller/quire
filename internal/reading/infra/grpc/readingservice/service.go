// Package readingservice registers the reading slice's gRPC service and hands
// each call to the controller that serves it.
//
// It is the whole of what the reference architecture calls the routes file. A
// gRPC service has no routing table — the generated interface is the table — so
// what remains is one forwarding method per call, and the value of keeping them
// here is that the list of what the slice serves is one file long.
//
// The Unimplemented struct is embedded because the contract requires it and
// because buf.gen.yaml says why. What that costs is that a forgotten method
// answers Unimplemented instead of failing to build, so a test calls all seven
// and refuses that answer.
package readingservice

import (
	"context"

	"google.golang.org/grpc"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/createannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/deleteannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/listannotations"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/listreadingprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/updateannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/updatereadingprogress"
)

// Controllers is every handler the service forwards to, which the slice's
// container fills.
type Controllers struct {
	// The five that serve UC04.
	CreateAnnotation *createannotation.CreateAnnotation
	GetAnnotation    *getannotation.GetAnnotation
	ListAnnotations  *listannotations.ListAnnotations
	UpdateAnnotation *updateannotation.UpdateAnnotation
	DeleteAnnotation *deleteannotation.DeleteAnnotation
	// The two that serve UC05.
	UpdateReadingProgress *updatereadingprogress.UpdateReadingProgress
	ListReadingProgress   *listreadingprogress.ListReadingProgress
}

// Service is the gRPC surface of the reading slice.
type Service struct {
	quirev1.UnimplementedReadingServiceServer

	controllers Controllers
}

// Service implements the generated server interface.
var _ quirev1.ReadingServiceServer = (*Service)(nil)

// New returns the service over its controllers.
func New(controllers *Controllers) *Service {
	return &Service{controllers: *controllers}
}

// Register publishes the service on the node's gRPC server.
func (s *Service) Register(registrar grpc.ServiceRegistrar) {
	quirev1.RegisterReadingServiceServer(registrar, s)
}

// CreateAnnotation records a mark (UC04).
func (s *Service) CreateAnnotation(
	ctx context.Context, request *quirev1.CreateAnnotationRequest,
) (*quirev1.CreateAnnotationResponse, error) {
	return s.controllers.CreateAnnotation.Handle(ctx, request)
}

// GetAnnotation answers with one mark (UC04).
func (s *Service) GetAnnotation(
	ctx context.Context, request *quirev1.GetAnnotationRequest,
) (*quirev1.GetAnnotationResponse, error) {
	return s.controllers.GetAnnotation.Handle(ctx, request)
}

// ListAnnotations answers with one page of what was written in a work (UC04).
func (s *Service) ListAnnotations(
	ctx context.Context, request *quirev1.ListAnnotationsRequest,
) (*quirev1.ListAnnotationsResponse, error) {
	return s.controllers.ListAnnotations.Handle(ctx, request)
}

// UpdateAnnotation edits a mark (UC04, RF03).
func (s *Service) UpdateAnnotation(
	ctx context.Context, request *quirev1.UpdateAnnotationRequest,
) (*quirev1.UpdateAnnotationResponse, error) {
	return s.controllers.UpdateAnnotation.Handle(ctx, request)
}

// DeleteAnnotation tombstones a mark (UC04).
func (s *Service) DeleteAnnotation(
	ctx context.Context, request *quirev1.DeleteAnnotationRequest,
) (*quirev1.DeleteAnnotationResponse, error) {
	return s.controllers.DeleteAnnotation.Handle(ctx, request)
}

// UpdateReadingProgress records where the calling device has reached (UC05).
func (s *Service) UpdateReadingProgress(
	ctx context.Context, request *quirev1.UpdateReadingProgressRequest,
) (*quirev1.UpdateReadingProgressResponse, error) {
	return s.controllers.UpdateReadingProgress.Handle(ctx, request)
}

// ListReadingProgress answers with every device's position (UC05, RN01).
func (s *Service) ListReadingProgress(
	ctx context.Context, request *quirev1.ListReadingProgressRequest,
) (*quirev1.ListReadingProgressResponse, error) {
	return s.controllers.ListReadingProgress.Handle(ctx, request)
}
