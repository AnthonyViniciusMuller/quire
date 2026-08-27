package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew   = "shared/httpx: new"
	opServe = "shared/httpx: serve"
)

// The paths the node serves for whoever operates it.
const (
	// LivenessPath answers whether the process is still able to answer.
	LivenessPath = "/healthz"
	// ReadinessPath answers whether traffic should be sent here now.
	ReadinessPath = "/readyz"
	// MetricsPath is where the Prometheus registry is exposed.
	MetricsPath = "/metrics"
)

// The timeouts of the server. They are constants rather than configuration
// because none of them is a deployment decision: they bound what a client may
// hold open, and every endpoint served here answers in milliseconds.
const (
	// readHeaderTimeout bounds the request line and the headers, which is what
	// closes a connection opened only to be left half-written.
	readHeaderTimeout = 5 * time.Second
	// readTimeout bounds the whole request.
	readTimeout = 10 * time.Second
	// writeTimeout bounds the answer. A scrape of a large registry is the
	// slowest thing served here and is still far inside it.
	writeTimeout = 15 * time.Second
	// idleTimeout closes a kept-alive connection nobody is using.
	idleTimeout = 60 * time.Second
	// maxHeaderBytes bounds the headers of one request.
	maxHeaderBytes = 64 << 10
)

// Server is the HTTP listener of the node.
type Server struct {
	server          *http.Server
	listener        net.Listener
	shutdownTimeout time.Duration
	logger          *slog.Logger
}

// settings is what the options accumulate before the server is built.
type settings struct {
	routes map[string]http.Handler
	probes []namedProbe
	logger *slog.Logger
}

// Option configures the server built by [New].
type Option func(*settings)

// WithHandler mounts handler at pattern, in the syntax net/http.ServeMux
// accepts — "GET /.well-known/quire/server" and not merely a path, so that a
// method nobody serves is answered with 405 rather than with the document.
func WithHandler(pattern string, handler http.Handler) Option {
	return func(s *settings) { s.routes[pattern] = handler }
}

// WithMetrics mounts handler at [MetricsPath]. It is an option rather than a
// fixture so that this package does not depend on a metrics implementation,
// and so that a deployment that publishes nothing simply omits it.
func WithMetrics(handler http.Handler) Option {
	return WithHandler("GET "+MetricsPath, handler)
}

// WithReadinessProbe adds a dependency [ReadinessPath] consults, under the
// name it is reported by.
func WithReadinessProbe(name string, probe Probe) Option {
	return func(s *settings) { s.probes = append(s.probes, namedProbe{name: name, probe: probe}) }
}

// WithLogger sets the logger of the server and of the readiness probes.
func WithLogger(logger *slog.Logger) Option {
	return func(s *settings) { s.logger = logger }
}

// New opens the listener described by cfg and builds the server behind it.
//
// The caller owns the result and must either [Server.Serve] it or
// [Server.Close] it.
func New(ctx context.Context, cfg *config.Server, opts ...Option) (*Server, error) {
	applied := settings{routes: map[string]http.Handler{}, logger: logging.Discard()}
	for _, opt := range opts {
		opt(&applied)
	}

	var listen net.ListenConfig

	listener, err := listen.Listen(ctx, "tcp", cfg.HTTPAddress)
	if err != nil {
		return nil, errs.Wrapf(err, errs.KindUnavailable,
			"the http address %s could not be bound", cfg.HTTPAddress).WithOp(opNew)
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+LivenessPath, livenessHandler())
	mux.Handle("GET "+ReadinessPath, readinessHandler(applied.probes, applied.logger))

	for pattern, handler := range applied.routes {
		mux.Handle(pattern, handler)
	}

	return &Server{
		server: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
			// Without this the server writes its own failures to the standard
			// logger, which is the one stream the node does not collect.
			ErrorLog: slog.NewLogLogger(applied.logger.Handler(), slog.LevelWarn),
		},
		listener:        listener,
		shutdownTimeout: cfg.ShutdownTimeout,
		logger:          applied.logger,
	}, nil
}

// Addr is the address the server is listening on.
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Serve answers requests until ctx is canceled or the listener fails.
//
// Cancelling ctx stops the server gracefully, bounded by the configured
// shutdown timeout: the listener closes at once and the requests in flight are
// given that long to finish.
func (s *Server) Serve(ctx context.Context) error {
	served := make(chan error, 1)

	go func() { served <- s.server.Serve(s.listener) }()

	select {
	case err := <-served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errs.Wrap(err, errs.KindUnavailable, "the http server stopped accepting requests").WithOp(opServe)
		}

		return nil
	case <-ctx.Done():
		s.shutdown(context.WithoutCancel(ctx))
		<-served

		return nil
	}
}

// Close stops the server without waiting, and releases the listener. It is
// safe after Serve has returned, so a caller may defer it.
func (s *Server) Close() { _ = s.server.Close() }

// shutdown stops the server gracefully and stops waiting once the shutdown
// timeout has passed.
func (s *Server) shutdown(ctx context.Context) {
	stopping, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(stopping); err != nil {
		s.logger.WarnContext(ctx, "http graceful shutdown timed out, closing the requests still running",
			logging.Err(err))

		_ = s.server.Close()
	}
}
