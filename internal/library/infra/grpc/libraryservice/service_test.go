package libraryservice_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identityapptest "github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	addtocollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/addtocollection"
	createcollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/createcollection"
	createebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/createebook"
	deletecollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deletecollection"
	deleteebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deleteebook"
	downloadcontentusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/downloadcontent"
	getcollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	getebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	listcollectionsusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/listcollections"
	listebooksusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/listebooks"
	removefromcollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/removefromcollection"
	updatecollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/updatecollection"
	updateebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/updateebook"
	uploadcontentusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/uploadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/addebooktocollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/createcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/createebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/deletecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/deleteebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/downloadebookcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/listcollections"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/listebooks"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/removeebookfromcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/updatecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/updateebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/uploadebookcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/libraryservice"
)

// errReached is what a recorder reports instead of doing the work: the call
// got this far, and the test is about how far that is.
var errReached = errors.New("the use case was reached")

// recorder stands in for a use case and writes down that it ran.
type recorder[In, Out any] struct {
	name  string
	calls *[]string
}

func (r recorder[In, Out]) Execute(_ context.Context, _ In) (Out, error) {
	var zero Out

	*r.calls = append(*r.calls, r.name)

	return zero, errReached
}

// TestEveryCallReachesItsController is what the embedded Unimplemented struct
// costs, paid back.
//
// buf.gen.yaml keeps that embedding on purpose, so a method left out of this
// service compiles and answers Unimplemented rather than failing to build.
// This calls all fourteen and refuses that answer — and, because each stand-in
// has a name, it also refuses a forwarding method wired to the wrong
// controller, which is the mistake a file of fourteen near-identical methods
// invites.
func TestEveryCallReachesItsController(t *testing.T) {
	t.Parallel()

	var calls []string

	service := libraryservice.New(&libraryservice.Controllers{
		CreateEbook: createebook.New(
			recorder[createebookusecase.Input, createebookusecase.Output]{name: "CreateEbook", calls: &calls}),
		GetEbook: getebook.New(
			recorder[getebookusecase.Input, getebookusecase.Output]{name: "GetEbook", calls: &calls}),
		ListEbooks: listebooks.New(
			recorder[listebooksusecase.Input, listebooksusecase.Output]{name: "ListEbooks", calls: &calls}),
		UpdateEbook: updateebook.New(
			recorder[updateebookusecase.Input, updateebookusecase.Output]{name: "UpdateEbook", calls: &calls}),
		DeleteEbook: deleteebook.New(
			recorder[deleteebookusecase.Input, deleteebookusecase.Output]{name: "DeleteEbook", calls: &calls}),
		UploadEbookContent: uploadebookcontent.New(
			recorder[uploadcontentusecase.Input, uploadcontentusecase.Output]{
				name: "UploadEbookContent", calls: &calls,
			}),
		DownloadEbookContent: downloadebookcontent.New(
			recorder[downloadcontentusecase.Input, downloadcontentusecase.Output]{
				name: "DownloadEbookContent", calls: &calls,
			}),
		CreateCollection: createcollection.New(
			recorder[createcollectionusecase.Input, createcollectionusecase.Output]{
				name: "CreateCollection", calls: &calls,
			}),
		GetCollection: getcollection.New(
			recorder[getcollectionusecase.Input, getcollectionusecase.Output]{
				name: "GetCollection", calls: &calls,
			}),
		ListCollections: listcollections.New(
			recorder[listcollectionsusecase.Input, listcollectionsusecase.Output]{
				name: "ListCollections", calls: &calls,
			}),
		UpdateCollection: updatecollection.New(
			recorder[updatecollectionusecase.Input, updatecollectionusecase.Output]{
				name: "UpdateCollection", calls: &calls,
			}),
		DeleteCollection: deletecollection.New(
			recorder[deletecollectionusecase.Input, deletecollectionusecase.Output]{
				name: "DeleteCollection", calls: &calls,
			}),
		AddEbookToCollection: addebooktocollection.New(
			recorder[addtocollectionusecase.Input, addtocollectionusecase.Output]{
				name: "AddEbookToCollection", calls: &calls,
			}),
		RemoveEbookFromCollection: removeebookfromcollection.New(
			recorder[removefromcollectionusecase.Input, removefromcollectionusecase.Output]{
				name: "RemoveEbookFromCollection", calls: &calls,
			}),
	})

	ctx := authenticated(t)
	ebookID, collectionID := uuid.New().String(), uuid.New().String()
	digest := "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

	tests := []struct {
		name string
		call func() error
	}{
		{"CreateEbook", func() error {
			_, err := service.CreateEbook(ctx, &quirev1.CreateEbookRequest{Ebook: &quirev1.Ebook{}})

			return err
		}},
		{"GetEbook", func() error {
			_, err := service.GetEbook(ctx, &quirev1.GetEbookRequest{EbookId: ebookID})

			return err
		}},
		{"ListEbooks", func() error {
			_, err := service.ListEbooks(ctx, &quirev1.ListEbooksRequest{})

			return err
		}},
		{"UpdateEbook", func() error {
			_, err := service.UpdateEbook(ctx, &quirev1.UpdateEbookRequest{
				EbookId:    ebookID,
				Ebook:      &quirev1.Ebook{},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			})

			return err
		}},
		{"DeleteEbook", func() error {
			_, err := service.DeleteEbook(ctx, &quirev1.DeleteEbookRequest{EbookId: ebookID})

			return err
		}},
		{"UploadEbookContent", func() error {
			//nolint:contextcheck // the context travels in the stream, which is what a stream is.
			return service.UploadEbookContent(&uploadStream{
				ctx: ctx,
				messages: []*quirev1.UploadEbookContentRequest{{
					Payload: &quirev1.UploadEbookContentRequest_Content{
						Content: &quirev1.EbookContent{
							ContentHash: digest, SizeBytes: 3, MediaType: "application/epub+zip",
						},
					},
				}},
			})
		}},
		{"DownloadEbookContent", func() error {
			return service.DownloadEbookContent(
				&quirev1.DownloadEbookContentRequest{EbookId: ebookID}, &downloadStream{ctx: ctx})
		}},
		{"CreateCollection", func() error {
			_, err := service.CreateCollection(ctx,
				&quirev1.CreateCollectionRequest{Collection: &quirev1.Collection{}})

			return err
		}},
		{"GetCollection", func() error {
			_, err := service.GetCollection(ctx,
				&quirev1.GetCollectionRequest{CollectionId: collectionID})

			return err
		}},
		{"ListCollections", func() error {
			_, err := service.ListCollections(ctx, &quirev1.ListCollectionsRequest{})

			return err
		}},
		{"UpdateCollection", func() error {
			_, err := service.UpdateCollection(ctx, &quirev1.UpdateCollectionRequest{
				CollectionId: collectionID,
				Collection:   &quirev1.Collection{},
				UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			})

			return err
		}},
		{"DeleteCollection", func() error {
			_, err := service.DeleteCollection(ctx,
				&quirev1.DeleteCollectionRequest{CollectionId: collectionID})

			return err
		}},
		{"AddEbookToCollection", func() error {
			_, err := service.AddEbookToCollection(ctx, &quirev1.AddEbookToCollectionRequest{
				EbookId: ebookID, CollectionId: collectionID,
			})

			return err
		}},
		{"RemoveEbookFromCollection", func() error {
			_, err := service.RemoveEbookFromCollection(ctx, &quirev1.RemoveEbookFromCollectionRequest{
				EbookId: ebookID, CollectionId: collectionID,
			})

			return err
		}},
	}

	for _, test := range tests {
		calls = calls[:0]

		err := test.call()

		if status.Code(err) == codes.Unimplemented {
			t.Errorf("%s answers Unimplemented, so the service does not serve it", test.name)

			continue
		}

		if !errors.Is(err, errReached) {
			t.Errorf("%s did not reach a use case: %v", test.name, err)

			continue
		}

		if len(calls) != 1 || calls[0] != test.name {
			t.Errorf("%s reached %v, want its own controller", test.name, calls)
		}
	}
}

// uploadStream is a client stream that serves the messages a test gave it and
// then ends, which is what a well-formed upload looks like from the server.
type uploadStream struct {
	grpc.ServerStream

	// A stream carries its own context rather than taking one; these stand
	// in for the one the server would have set up.
	ctx      context.Context
	messages []*quirev1.UploadEbookContentRequest
	sent     int
}

func (s *uploadStream) Context() context.Context { return s.ctx }

func (s *uploadStream) Recv() (*quirev1.UploadEbookContentRequest, error) {
	if s.sent >= len(s.messages) {
		return nil, io.EOF
	}

	message := s.messages[s.sent]
	s.sent++

	return message, nil
}

func (s *uploadStream) SendAndClose(*quirev1.UploadEbookContentResponse) error { return nil }

// downloadStream is a server stream that discards what it is sent.
type downloadStream struct {
	grpc.ServerStream

	ctx context.Context
}

func (s *downloadStream) Context() context.Context { return s.ctx }

func (s *downloadStream) Send(*quirev1.DownloadEbookContentResponse) error { return nil }

// authenticated is a context carrying an identity, built by running the real
// interceptor rather than by reaching into it.
//
// The interceptor belongs to the identity slice, which is the only part of the
// node that can verify a token, and every slice's controllers read the identity
// it stamps. This one does the same thing the node does.
func authenticated(t *testing.T) context.Context {
	t.Helper()

	auth := identityapptest.NewAuthService()
	clock := identityapptest.NewClock(time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC))

	token, _, err := auth.IssueAccess(uuid.New(), uuid.New(), clock.Now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	incoming := metadata.NewIncomingContext(t.Context(), metadata.Pairs("authorization", "Bearer "+token))

	var served context.Context

	_, err = authn.New(auth, clock, nil).Unary()(incoming, nil,
		&grpc.UnaryServerInfo{FullMethod: quirev1.LibraryService_ListEbooks_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
