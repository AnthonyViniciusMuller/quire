package grpcx_test

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
)

// observed is what a served call reported about the context it ran under.
type observed struct {
	requestID string
	header    metadata.MD
}

// serveObserving starts a server behind the request interceptor whose stub
// records the request identifier it was served with.
func serveObserving(t *testing.T, seen *observed) healthpb.HealthClient {
	t.Helper()

	record := func(ctx context.Context) { seen.requestID = grpcx.RequestID(ctx) }

	server, err := grpcx.New(t.Context(), serverConfig(),
		grpcx.WithUnaryInterceptors(grpcx.UnaryRequestInterceptor()),
		grpcx.WithStreamInterceptors(grpcx.StreamRequestInterceptor()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	healthpb.RegisterHealthServer(server.Registrar(), &observingHealth{record: record})

	return healthpb.NewHealthClient(serve(t, server))
}

// observingHealth answers both methods and reports the context it ran under.
type observingHealth struct {
	healthpb.UnimplementedHealthServer

	record func(ctx context.Context)
}

func (h *observingHealth) Check(
	ctx context.Context, _ *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckResponse, error) {
	h.record(ctx)

	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func (h *observingHealth) Watch(_ *healthpb.HealthCheckRequest, stream healthpb.Health_WatchServer) error {
	h.record(stream.Context())

	return nil
}

func TestRequestInterceptorStampsAnIdentifierAndEchoesIt(t *testing.T) {
	t.Parallel()

	var seen observed

	client := serveObserving(t, &seen)

	if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{},
		grpc.Header(&seen.header)); err != nil {
		t.Fatalf("Check: %v", err)
	}

	if _, err := uuid.Parse(seen.requestID); err != nil {
		t.Errorf("the handler was served with %q, want a generated uuid", seen.requestID)
	}

	echoed := seen.header.Get(grpcx.RequestIDHeader)
	if len(echoed) != 1 || echoed[0] != seen.requestID {
		t.Errorf("the response header carries %v, want the identifier %q", echoed, seen.requestID)
	}
}

func TestRequestInterceptorReusesTheIdentifierTheCallerSupplied(t *testing.T) {
	t.Parallel()

	const supplied = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

	var seen observed

	client := serveObserving(t, &seen)
	ctx := metadata.AppendToOutgoingContext(t.Context(), grpcx.RequestIDHeader, supplied)

	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	if seen.requestID != supplied {
		t.Errorf("the handler was served with %q, want the supplied %q", seen.requestID, supplied)
	}
}

func TestRequestInterceptorRefusesAnIdentifierItCannotLog(t *testing.T) {
	t.Parallel()

	// The interceptor is called directly rather than through a client,
	// because grpc-go refuses to send a header value with a control character
	// in it — which is most of what this guard is for. The guard stays: the
	// callers of a federated node are not all grpc-go, and the value ends up
	// in a log stream where one line can be made to look like two.
	cases := map[string]string{
		"a newline, which would forge a second log line": "abc\ndef",
		"a tab":                          "abc\tdef",
		"a space":                        "two words",
		"longer than an identifier gets": strings.Repeat("a", 65),
		"empty":                          "   ",
	}

	for name, supplied := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var served string

			handler := func(ctx context.Context, _ any) (any, error) {
				served = grpcx.RequestID(ctx)

				return nil, nil
			}

			ctx := metadata.NewIncomingContext(t.Context(),
				metadata.Pairs(grpcx.RequestIDHeader, supplied))
			info := &grpc.UnaryServerInfo{FullMethod: "/quire.v1.SyncService/PushOperations"}

			if _, err := grpcx.UnaryRequestInterceptor()(ctx, nil, info, handler); err != nil {
				t.Fatalf("the interceptor failed: %v", err)
			}

			if served == supplied {
				t.Errorf("the handler was served with the supplied %q", supplied)
			}

			if _, err := uuid.Parse(served); err != nil {
				t.Errorf("the handler was served with %q, want a generated uuid", served)
			}
		})
	}
}

func TestStreamRequestInterceptorReachesTheHandlerContext(t *testing.T) {
	t.Parallel()

	var seen observed

	client := serveObserving(t, &seen)

	stream, err := client.Watch(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	if _, err := stream.Recv(); err == nil {
		t.Fatal("the stream did not end")
	}

	if _, err := uuid.Parse(seen.requestID); err != nil {
		t.Errorf("the handler was served with %q, want a generated uuid", seen.requestID)
	}
}

func TestRequestIDIsEmptyOutsideACall(t *testing.T) {
	t.Parallel()

	if id := grpcx.RequestID(t.Context()); id != "" {
		t.Errorf("RequestID is %q outside a call, want empty", id)
	}
}
