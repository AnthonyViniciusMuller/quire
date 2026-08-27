package metrics_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/metrics"
)

// scrape returns the exposition the registry would answer a Prometheus scrape
// with.
func scrape(t *testing.T, registry *metrics.Registry) string {
	t.Helper()

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))

	response := recorder.Result()
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("the scrape answered %d", response.StatusCode)
	}

	read, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the scrape: %v", err)
	}

	return string(read)
}

func TestRegistryPublishesTheRuntimeAndProcessCollectors(t *testing.T) {
	t.Parallel()

	registry, err := metrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	exposition := scrape(t, registry)

	for _, metric := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_open_fds"} {
		if !strings.Contains(exposition, metric) {
			t.Errorf("the scrape does not publish %s", metric)
		}
	}
}

// The latency requirement RNF06 is 200 ms, and a histogram can only answer it
// exactly if 200 ms is one of its boundaries.
func TestRegistryPutsTheLatencyRequirementOnABucketBoundary(t *testing.T) {
	t.Parallel()

	registry, healthClient := serveMeasured(t, &healthStub{})

	if _, err := healthClient.Check(t.Context(), &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	exposition := scrape(t, registry)

	if !strings.Contains(exposition, `grpc_server_handling_seconds_bucket`) {
		t.Fatal("the scrape publishes no latency histogram")
	}

	if !strings.Contains(exposition, `le="0.2"`) {
		t.Error("200 ms is not a bucket boundary, so RNF06 could only be read off it by interpolation")
	}
}

func TestRegistryCountsACallTheChainRejected(t *testing.T) {
	t.Parallel()

	registry, healthClient := serveMeasured(t, &healthStub{
		err: errs.New(errs.KindNotFound, "no such e-book"),
	})

	if _, err := healthClient.Check(t.Context(), &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("Check succeeded")
	}

	exposition := scrape(t, registry)

	// The interceptor runs outside the translation, so what it counted is the
	// code the client received and not the kind the handler returned.
	if !strings.Contains(exposition, `grpc_code="NotFound"`) {
		t.Errorf("the scrape does not count a NotFound call:\n%s", exposition)
	}
}

func TestInitializeGRPCPublishesAMethodNobodyCalled(t *testing.T) {
	t.Parallel()

	registry, _ := serveMeasured(t, &healthStub{})

	exposition := scrape(t, registry)

	// Watch is never called by this test. Without the pre-registration it
	// would have no series at all, and a rate over it would return nothing
	// rather than zero.
	if !strings.Contains(exposition, `grpc_method="Watch"`) {
		t.Error("a method nobody called has no series, so an alert over it can never fire")
	}
}

// healthStub answers whatever the test asked for.
type healthStub struct {
	healthpb.UnimplementedHealthServer

	err error
}

func (h *healthStub) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, h.err
}

// serveMeasured starts a gRPC server behind the whole chain with the metric
// interceptors around it, and returns the registry and a client.
func serveMeasured(t *testing.T, service *healthStub) (*metrics.Registry, healthpb.HealthClient) {
	t.Helper()

	registry, err := metrics.New()
	if err != nil {
		t.Fatalf("metrics.New: %v", err)
	}

	chain := grpcx.NewChain(logging.Discard()).Around(registry.GRPCServerInterceptors())

	server, err := grpcx.New(t.Context(),
		&config.Server{GRPCAddress: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second},
		grpcx.WithChain(chain))
	if err != nil {
		t.Fatalf("grpcx.New: %v", err)
	}

	healthpb.RegisterHealthServer(server.Registrar(), service)
	registry.InitializeGRPC(server)

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
			t.Errorf("Serve returned %v", err)
		}
	})

	return registry, healthpb.NewHealthClient(conn)
}
