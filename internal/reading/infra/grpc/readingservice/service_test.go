package readingservice_test

import (
	"context"
	"errors"
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
	createannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/createannotation"
	deleteannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/deleteannotation"
	getannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/getannotation"
	listannotationsusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listannotations"
	listprogressusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listprogress"
	updateannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateannotation"
	updateprogressusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/createannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/deleteannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/listannotations"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/listreadingprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/updateannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/updatereadingprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/readingservice"
)

// errReached is what a recorder reports instead of doing the work: the call got
// this far, and the test is about how far that is.
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
// service compiles and answers Unimplemented rather than failing to build. This
// calls all seven and refuses that answer — and, because each stand-in has a
// name, it also refuses a forwarding method wired to the wrong controller,
// which is the mistake a file of near-identical methods invites.
func TestEveryCallReachesItsController(t *testing.T) {
	t.Parallel()

	var calls []string

	service := readingservice.New(&readingservice.Controllers{
		CreateAnnotation: createannotation.New(
			recorder[createannotationusecase.Input, createannotationusecase.Output]{
				name: "CreateAnnotation", calls: &calls,
			}),
		GetAnnotation: getannotation.New(
			recorder[getannotationusecase.Input, getannotationusecase.Output]{
				name: "GetAnnotation", calls: &calls,
			}),
		ListAnnotations: listannotations.New(
			recorder[listannotationsusecase.Input, listannotationsusecase.Output]{
				name: "ListAnnotations", calls: &calls,
			}),
		UpdateAnnotation: updateannotation.New(
			recorder[updateannotationusecase.Input, updateannotationusecase.Output]{
				name: "UpdateAnnotation", calls: &calls,
			}),
		DeleteAnnotation: deleteannotation.New(
			recorder[deleteannotationusecase.Input, deleteannotationusecase.Output]{
				name: "DeleteAnnotation", calls: &calls,
			}),
		UpdateReadingProgress: updatereadingprogress.New(
			recorder[updateprogressusecase.Input, updateprogressusecase.Output]{
				name: "UpdateReadingProgress", calls: &calls,
			}),
		ListReadingProgress: listreadingprogress.New(
			recorder[listprogressusecase.Input, listprogressusecase.Output]{
				name: "ListReadingProgress", calls: &calls,
			}),
	})

	ctx := authenticated(t)
	ebookID, annotationID := uuid.New().String(), uuid.New().String()

	tests := []struct {
		name string
		call func() error
	}{
		{"CreateAnnotation", func() error {
			_, err := service.CreateAnnotation(ctx, &quirev1.CreateAnnotationRequest{
				Annotation: &quirev1.Annotation{EbookId: ebookID},
			})

			return err
		}},
		{"GetAnnotation", func() error {
			_, err := service.GetAnnotation(ctx,
				&quirev1.GetAnnotationRequest{AnnotationId: annotationID})

			return err
		}},
		{"ListAnnotations", func() error {
			_, err := service.ListAnnotations(ctx,
				&quirev1.ListAnnotationsRequest{EbookId: ebookID})

			return err
		}},
		{"UpdateAnnotation", func() error {
			_, err := service.UpdateAnnotation(ctx, &quirev1.UpdateAnnotationRequest{
				AnnotationId: annotationID,
				Annotation:   &quirev1.Annotation{},
				UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"text"}},
			})

			return err
		}},
		{"DeleteAnnotation", func() error {
			_, err := service.DeleteAnnotation(ctx,
				&quirev1.DeleteAnnotationRequest{AnnotationId: annotationID})

			return err
		}},
		{"UpdateReadingProgress", func() error {
			_, err := service.UpdateReadingProgress(ctx, &quirev1.UpdateReadingProgressRequest{
				EbookId: ebookID, Locator: "page=42",
			})

			return err
		}},
		{"ListReadingProgress", func() error {
			_, err := service.ListReadingProgress(ctx,
				&quirev1.ListReadingProgressRequest{EbookId: ebookID})

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
		&grpc.UnaryServerInfo{FullMethod: quirev1.ReadingService_ListAnnotations_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
