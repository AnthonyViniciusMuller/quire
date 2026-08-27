package grpcx_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
)

// serverConfig binds to port zero on the loopback, so that the tests never
// collide with each other or with whatever else the machine is running.
func serverConfig() *config.Server {
	return &config.Server{GRPCAddress: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second}
}

// serve starts server in the background and returns a client connected to it.
// Both are torn down when the test ends.
func serve(t *testing.T, server *grpcx.Server) *grpc.ClientConn {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	conn, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cancel()
		t.Fatalf("dialing the server: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		cancel()

		if err := <-served; err != nil {
			t.Errorf("Serve returned %v, want nil after cancellation", err)
		}
	})

	return conn
}

func TestServerServesARegisteredService(t *testing.T) {
	t.Parallel()

	server, err := grpcx.New(t.Context(), serverConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	healthpb.RegisterHealthServer(server.Registrar(), health.NewServer())

	client := healthpb.NewHealthClient(serve(t, server))

	response, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("status is %s, want SERVING", response.GetStatus())
	}
}

func TestServerRunsTheInterceptorsOutermostFirst(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		called []string
	)

	record := func(name string) grpc.UnaryServerInterceptor {
		return func(
			ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
		) (any, error) {
			mu.Lock()
			called = append(called, name)
			mu.Unlock()

			return handler(ctx, req)
		}
	}

	server, err := grpcx.New(t.Context(), serverConfig(),
		grpcx.WithUnaryInterceptors(record("outer")),
		grpcx.WithUnaryInterceptors(record("inner")),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	healthpb.RegisterHealthServer(server.Registrar(), health.NewServer())

	client := healthpb.NewHealthClient(serve(t, server))
	if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(called) != 2 || called[0] != "outer" || called[1] != "inner" {
		t.Errorf("the chain ran as %v, want [outer inner]", called)
	}
}

func TestServerRegistersReflectionOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	const reflectionService = "grpc.reflection.v1.ServerReflection"

	for name, enabled := range map[string]bool{"enabled": true, "disabled": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server, err := grpcx.New(t.Context(), serverConfig(), grpcx.WithReflection(enabled))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer server.Close()

			_, registered := server.GetServiceInfo()[reflectionService]
			if registered != enabled {
				t.Errorf("reflection registered is %t, want %t", registered, enabled)
			}
		})
	}
}

func TestNewReportsAnAddressItCannotBind(t *testing.T) {
	t.Parallel()

	// Holding the port is the portable way to make the bind fail: a reserved
	// port number would only fail for an unprivileged process.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port: %v", err)
	}
	defer func() { _ = taken.Close() }()

	cfg := serverConfig()
	cfg.GRPCAddress = taken.Addr().String()

	server, err := grpcx.New(t.Context(), cfg)
	if err == nil {
		server.Close()
		t.Fatal("New succeeded on a port already in use")
	}

	if !errors.Is(err, errs.KindUnavailable) {
		t.Errorf("New failed with %v, want a %s error", err, errs.KindUnavailable)
	}
}

func TestServeReturnsWhenTheContextIsCanceled(t *testing.T) {
	t.Parallel()

	server, err := grpcx.New(t.Context(), serverConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	// The listener is open before Serve runs, so cancelling immediately still
	// exercises the shutdown rather than racing it.
	cancel()

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the context was canceled")
	}

	if _, err := net.DialTimeout("tcp", server.Addr().String(), time.Second); err == nil {
		t.Error("the listener still accepts connections after the shutdown")
	}
}
