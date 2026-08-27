// Package metrics is the measurement surface of the node: one registry, the
// collectors any Go process should expose, and the gRPC server metrics the
// latency requirement is read from.
//
// The registry is built here rather than taken from the Prometheus default,
// which is a package-level variable every imported library may write into. A
// node that owns its registry knows exactly what it publishes, and a test can
// hold one of its own without the metrics of the previous test still in it.
package metrics

import (
	"net/http"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const opNew = "shared/metrics: new"

// latencyBuckets are the upper bounds of the gRPC latency histogram, in
// seconds.
//
// The list is the Prometheus default with 0.15 and 0.2 inserted, and 0.2 is
// the whole reason: RNF06 budgets 200 ms for a synchronization over a stable
// connection, so the fraction of calls that met the requirement has to be a
// bucket boundary. Read off a boundary it is a count; read off the default
// buckets, where 200 ms falls between 0.1 and 0.25, it would be a linear
// interpolation inside a bucket — an estimate presented as evidence, and the
// first thing worth questioning about it.
func latencyBuckets() []float64 {
	return []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.2, 0.3, 0.5, 1, 2.5, 5, 10}
}

// Registry is everything the node measures about itself.
type Registry struct {
	registry *prometheus.Registry
	grpc     *grpcprom.ServerMetrics
}

// New builds the registry with the runtime, process and gRPC collectors
// already in it.
func New() (*Registry, error) {
	registry := prometheus.NewRegistry()

	serverMetrics := grpcprom.NewServerMetrics(
		grpcprom.WithServerHandlingTimeHistogram(grpcprom.WithHistogramBuckets(latencyBuckets())),
	)

	for _, collector := range []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		serverMetrics,
	} {
		if err := registry.Register(collector); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "a metric collector could not be registered").WithOp(opNew)
		}
	}

	return &Registry{registry: registry, grpc: serverMetrics}, nil
}

// Handler serves the registry in the Prometheus exposition format.
//
// It is not authenticated. The endpoint belongs to the operator of the node
// and is reachable only from inside the mesh, which the Istio authorization
// policy is what enforces; publishing it would tell a stranger the shape of
// the traffic and the version of the process.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{
		// A collector that fails describes the failure in the scrape rather
		// than failing the whole scrape, so one broken metric does not blind
		// the operator to the rest.
		ErrorHandling:     promhttp.ContinueOnError,
		EnableOpenMetrics: true,
	})
}

// Gatherer exposes the registry to a test, and to whatever else has to read
// the metrics without going through HTTP.
func (r *Registry) Gatherer() prometheus.Gatherer { return r.registry }

// GRPCServerInterceptors counts and times every call. They belong outermost in
// the chain: a call rejected by any interceptor below still has to be counted,
// or the rejection is invisible in exactly the measurement meant to show it.
func (r *Registry) GRPCServerInterceptors() (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	return r.grpc.UnaryServerInterceptor(), r.grpc.StreamServerInterceptor()
}

// InitializeGRPC publishes a zero for every method of every service the
// provider registered.
//
// Without it a method that has not been called yet has no series at all, and a
// rate over it returns nothing rather than zero — so the first failure of a
// rarely used method looks the same to an alert as a method that was never
// deployed. It has to run after every service is registered.
func (r *Registry) InitializeGRPC(provider reflection.ServiceInfoProvider) {
	r.grpc.InitializeMetrics(provider)
}
