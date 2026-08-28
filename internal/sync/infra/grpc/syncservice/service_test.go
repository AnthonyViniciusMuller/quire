package syncservice_test

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

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identityapptest "github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	pulloperationsusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pulloperations"
	pushoperationsusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/pulloperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/pushoperations"
	syncstream "github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/sync"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/syncservice"
	changesservice "github.com/anthonyvsmuller/quire/internal/sync/infra/service/changes"
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
// calls all three that are served and refuses that answer — and, because each
// stand-in has a name, it also refuses a forwarding method wired to the wrong
// controller.
func TestEveryCallReachesItsController(t *testing.T) {
	t.Parallel()

	var calls []string

	service := newService(&calls)
	ctx := authenticated(t)

	tests := []struct {
		name string
		call func() error
	}{
		{"PushOperations", func() error {
			_, err := service.PushOperations(ctx, &quirev1.PushOperationsRequest{})

			return err
		}},
		{"PullOperations", func() error {
			_, err := service.PullOperations(ctx, &quirev1.PullOperationsRequest{})

			return err
		}},
		{"Sync", func() error {
			stream := newStream(ctx)
			stream.offer(&quirev1.SyncRequest{
				Payload: &quirev1.SyncRequest_Start{Start: &quirev1.SyncStart{}},
			})
			stream.close()

			//nolint:contextcheck // the stream carries the context, which is what a stream is.
			return service.Sync(stream)
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

		if len(calls) == 0 {
			t.Errorf("%s reached no use case at all", test.name)
		}
	}
}

// ReplicateOperations is the one method of this contract whose caller is a peer
// node and not a device, and it is authenticated by a certificate rather than
// by a token. It is not served yet, and a test names that so it is a decision
// rather than an omission.
func TestReplicateOperationsIsNotServedYet(t *testing.T) {
	t.Parallel()

	var calls []string

	_, err := newService(&calls).ReplicateOperations(
		authenticated(t), &quirev1.ReplicateOperationsRequest{})

	if status.Code(err) != codes.Unimplemented {
		t.Errorf("ReplicateOperations = %v, want Unimplemented until the peer-facing half lands", err)
	}
}

// newService is the service over stand-ins that only record.
func newService(calls *[]string) *syncservice.Service {
	push := recorder[pushoperationsusecase.Input, pushoperationsusecase.Output]{
		name: "PushOperations", calls: calls,
	}
	pull := recorder[pulloperationsusecase.Input, pulloperationsusecase.Output]{
		name: "PullOperations", calls: calls,
	}

	return syncservice.New(&syncservice.Controllers{
		PushOperations: pushoperations.New(push),
		PullOperations: pulloperations.New(pull),
		Sync:           syncstream.New(push, pull, changesservice.New(), time.Hour),
	})
}

// stream is a bidirectional stream a test drives from both ends.
type stream struct {
	grpc.ServerStream

	ctx      context.Context
	incoming chan *quirev1.SyncRequest
	outgoing chan *quirev1.SyncResponse
}

func newStream(ctx context.Context) *stream {
	return &stream{
		ctx:      ctx,
		incoming: make(chan *quirev1.SyncRequest, 8),
		outgoing: make(chan *quirev1.SyncResponse, 8),
	}
}

// Context is the context the call is served under.
func (s *stream) Context() context.Context { return s.ctx }

// Recv reads what the test offered, and reports the half-close as the driver
// does.
func (s *stream) Recv() (*quirev1.SyncRequest, error) {
	request, open := <-s.incoming
	if !open {
		return nil, io.EOF
	}

	return request, nil
}

// Send records what the node wrote.
func (s *stream) Send(response *quirev1.SyncResponse) error {
	s.outgoing <- response

	return nil
}

// offer queues a message from the device.
func (s *stream) offer(request *quirev1.SyncRequest) { s.incoming <- request }

// close half-closes the device's end.
func (s *stream) close() { close(s.incoming) }

// authenticated is a context carrying an identity, built by running the real
// interceptor rather than by reaching into it.
func authenticated(t *testing.T) context.Context {
	t.Helper()

	auth := identityapptest.NewAuthService()
	clock := identityapptest.NewClock(time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC))

	token, _, err := auth.IssueAccess(uuid.New(), uuid.New(), clock.Now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	incoming := metadata.NewIncomingContext(t.Context(),
		metadata.Pairs("authorization", "Bearer "+token))

	var served context.Context

	_, err = authn.New(auth, clock, nil).Unary()(incoming, nil,
		&grpc.UnaryServerInfo{FullMethod: quirev1.SyncService_PullOperations_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
