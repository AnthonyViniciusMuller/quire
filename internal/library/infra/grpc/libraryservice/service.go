// Package libraryservice registers the library slice's gRPC service and hands
// each call to the controller that serves it.
//
// It is the whole of what the reference architecture calls the routes file. A
// gRPC service has no routing table — the generated interface is the table —
// so what remains is one forwarding method per call, and the value of keeping
// them here is that the list of what the slice serves is one file long.
//
// The Unimplemented struct is embedded because the contract requires it and
// because buf.gen.yaml says why. What that costs is that a forgotten method
// answers Unimplemented instead of failing to build, so a test calls all
// fourteen and refuses that answer.
package libraryservice

import (
	"context"

	"google.golang.org/grpc"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/addebooktocollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/beginebookupload"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/createcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/createebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/deletecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/deleteebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/discardebookupload"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/downloadebookcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/finishebookupload"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/listcollections"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/listebooks"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/putebookchunk"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/removeebookfromcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/updatecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/updateebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/uploadebookcontent"
)

// Controllers is every handler the service forwards to, which the slice's
// container fills.
type Controllers struct {
	// The five that serve UC01.
	CreateEbook *createebook.CreateEbook
	GetEbook    *getebook.GetEbook
	ListEbooks  *listebooks.ListEbooks
	UpdateEbook *updateebook.UpdateEbook
	DeleteEbook *deleteebook.DeleteEbook
	// The two that carry the file, which is UC02 and the read that answers it.
	UploadEbookContent *uploadebookcontent.UploadEbookContent
	// The four that serve UC02 for a caller which cannot open a client
	// stream — a browser, since gRPC-Web carries none (D10, D11). They
	// receive the same file the one above does, in pieces.
	BeginEbookUpload     *beginebookupload.BeginEbookUpload
	PutEbookChunk        *putebookchunk.PutEbookChunk
	FinishEbookUpload    *finishebookupload.FinishEbookUpload
	DiscardEbookUpload   *discardebookupload.DiscardEbookUpload
	DownloadEbookContent *downloadebookcontent.DownloadEbookContent
	// The seven that serve UC03.
	CreateCollection          *createcollection.CreateCollection
	GetCollection             *getcollection.GetCollection
	ListCollections           *listcollections.ListCollections
	UpdateCollection          *updatecollection.UpdateCollection
	DeleteCollection          *deletecollection.DeleteCollection
	AddEbookToCollection      *addebooktocollection.AddEbookToCollection
	RemoveEbookFromCollection *removeebookfromcollection.RemoveEbookFromCollection
}

// Service is the gRPC surface of the library slice.
type Service struct {
	quirev1.UnimplementedLibraryServiceServer

	controllers Controllers
}

// Service implements the generated server interface.
var _ quirev1.LibraryServiceServer = (*Service)(nil)

// New returns the service over its controllers.
func New(controllers *Controllers) *Service {
	return &Service{controllers: *controllers}
}

// Register publishes the service on the node's gRPC server.
func (s *Service) Register(registrar grpc.ServiceRegistrar) {
	quirev1.RegisterLibraryServiceServer(registrar, s)
}

// CreateEbook records the metadata of a work (UC01).
func (s *Service) CreateEbook(
	ctx context.Context, request *quirev1.CreateEbookRequest,
) (*quirev1.CreateEbookResponse, error) {
	return s.controllers.CreateEbook.Handle(ctx, request)
}

// GetEbook answers with one work (UC01).
func (s *Service) GetEbook(
	ctx context.Context, request *quirev1.GetEbookRequest,
) (*quirev1.GetEbookResponse, error) {
	return s.controllers.GetEbook.Handle(ctx, request)
}

// ListEbooks answers with one page of the collection (UC01).
func (s *Service) ListEbooks(
	ctx context.Context, request *quirev1.ListEbooksRequest,
) (*quirev1.ListEbooksResponse, error) {
	return s.controllers.ListEbooks.Handle(ctx, request)
}

// UpdateEbook edits the description of a work (UC01, RF05).
func (s *Service) UpdateEbook(
	ctx context.Context, request *quirev1.UpdateEbookRequest,
) (*quirev1.UpdateEbookResponse, error) {
	return s.controllers.UpdateEbook.Handle(ctx, request)
}

// DeleteEbook tombstones a work (UC01).
func (s *Service) DeleteEbook(
	ctx context.Context, request *quirev1.DeleteEbookRequest,
) (*quirev1.DeleteEbookResponse, error) {
	return s.controllers.DeleteEbook.Handle(ctx, request)
}

// UploadEbookContent receives the bytes of a work (UC02).
func (s *Service) UploadEbookContent(stream quirev1.LibraryService_UploadEbookContentServer) error {
	return s.controllers.UploadEbookContent.Handle(stream)
}

// BeginEbookUpload agrees to receive a file that will arrive in pieces (UC02).
func (s *Service) BeginEbookUpload(
	ctx context.Context, request *quirev1.BeginEbookUploadRequest,
) (*quirev1.BeginEbookUploadResponse, error) {
	return s.controllers.BeginEbookUpload.Handle(ctx, request)
}

// PutEbookChunk receives one piece of it (UC02).
func (s *Service) PutEbookChunk(
	ctx context.Context, request *quirev1.PutEbookChunkRequest,
) (*quirev1.PutEbookChunkResponse, error) {
	return s.controllers.PutEbookChunk.Handle(ctx, request)
}

// FinishEbookUpload checks what arrived and records it (UC02).
func (s *Service) FinishEbookUpload(
	ctx context.Context, request *quirev1.FinishEbookUploadRequest,
) (*quirev1.FinishEbookUploadResponse, error) {
	return s.controllers.FinishEbookUpload.Handle(ctx, request)
}

// DiscardEbookUpload abandons one (UC02).
func (s *Service) DiscardEbookUpload(
	ctx context.Context, request *quirev1.DiscardEbookUploadRequest,
) (*quirev1.DiscardEbookUploadResponse, error) {
	return s.controllers.DiscardEbookUpload.Handle(ctx, request)
}

// DownloadEbookContent streams the bytes back.
func (s *Service) DownloadEbookContent(
	request *quirev1.DownloadEbookContentRequest,
	stream quirev1.LibraryService_DownloadEbookContentServer,
) error {
	return s.controllers.DownloadEbookContent.Handle(request, stream)
}

// CreateCollection defines a grouping (UC03).
func (s *Service) CreateCollection(
	ctx context.Context, request *quirev1.CreateCollectionRequest,
) (*quirev1.CreateCollectionResponse, error) {
	return s.controllers.CreateCollection.Handle(ctx, request)
}

// GetCollection answers with one grouping (UC03).
func (s *Service) GetCollection(
	ctx context.Context, request *quirev1.GetCollectionRequest,
) (*quirev1.GetCollectionResponse, error) {
	return s.controllers.GetCollection.Handle(ctx, request)
}

// ListCollections answers with the reader's groupings (UC03).
func (s *Service) ListCollections(
	ctx context.Context, request *quirev1.ListCollectionsRequest,
) (*quirev1.ListCollectionsResponse, error) {
	return s.controllers.ListCollections.Handle(ctx, request)
}

// UpdateCollection edits a grouping (UC03).
func (s *Service) UpdateCollection(
	ctx context.Context, request *quirev1.UpdateCollectionRequest,
) (*quirev1.UpdateCollectionResponse, error) {
	return s.controllers.UpdateCollection.Handle(ctx, request)
}

// DeleteCollection tombstones a grouping (UC03).
func (s *Service) DeleteCollection(
	ctx context.Context, request *quirev1.DeleteCollectionRequest,
) (*quirev1.DeleteCollectionResponse, error) {
	return s.controllers.DeleteCollection.Handle(ctx, request)
}

// AddEbookToCollection files a work under a grouping (UC03).
func (s *Service) AddEbookToCollection(
	ctx context.Context, request *quirev1.AddEbookToCollectionRequest,
) (*quirev1.AddEbookToCollectionResponse, error) {
	return s.controllers.AddEbookToCollection.Handle(ctx, request)
}

// RemoveEbookFromCollection takes a work off a grouping (UC03).
func (s *Service) RemoveEbookFromCollection(
	ctx context.Context, request *quirev1.RemoveEbookFromCollectionRequest,
) (*quirev1.RemoveEbookFromCollectionResponse, error) {
	return s.controllers.RemoveEbookFromCollection.Handle(ctx, request)
}
