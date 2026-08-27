package grpcx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew   = "shared/grpcx: new"
	opServe = "shared/grpcx: serve"
)

// The keepalive settings of the server.
//
// A Quire client is a device on a mobile network holding a bidirectional sync
// stream open across long silences, and a middlebox on that path drops an idle
// connection without telling either end. The server therefore pings rather
// than waits: without it a node keeps writing operations into a stream nobody
// is reading, and the device only finds out when it next has something to say.
const (
	// keepaliveTime is how long a connection may stay idle before the server
	// pings it.
	keepaliveTime = 30 * time.Second
	// keepaliveTimeout is how long the server waits for the answer to that
	// ping before considering the connection dead.
	keepaliveTimeout = 10 * time.Second
	// minClientPingInterval is the fastest client ping the server tolerates.
	// Anything faster is answered with GOAWAY, which is what stops a
	// misbehaving client from turning keepalive into a flood.
	minClientPingInterval = 15 * time.Second
)

// baseServerOptions is how many options this package sets before the caller's
// own are appended: the two keepalive settings and the two chains.
const baseServerOptions = 4

// Server is the gRPC listener of the node.
//
// The listener is opened by [New] rather than by [Server.Serve], so that a port
// already in use fails while the process is still starting, and so that a test
// binding to port zero can read the address it actually got.
type Server struct {
	server          *grpc.Server
	listener        net.Listener
	shutdownTimeout time.Duration
	logger          *slog.Logger
}

// settings is what the options accumulate before the server is built.
type settings struct {
	unary         []grpc.UnaryServerInterceptor
	stream        []grpc.StreamServerInterceptor
	serverOptions []grpc.ServerOption
	reflection    bool
	logger        *slog.Logger
}

// Option configures the server built by [New].
type Option func(*settings)

// WithUnaryInterceptors appends interceptors to the unary chain, outermost
// first. Package grpcx documents the order the node uses and why.
func WithUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) Option {
	return func(s *settings) { s.unary = append(s.unary, interceptors...) }
}

// WithStreamInterceptors appends interceptors to the stream chain, outermost
// first.
func WithStreamInterceptors(interceptors ...grpc.StreamServerInterceptor) Option {
	return func(s *settings) { s.stream = append(s.stream, interceptors...) }
}

// WithServerOptions passes options straight to grpc.NewServer. It is the
// escape hatch for what this package deliberately does not model, transport
// credentials above all.
func WithServerOptions(options ...grpc.ServerOption) Option {
	return func(s *settings) { s.serverOptions = append(s.serverOptions, options...) }
}

// WithReflection publishes the server reflection service, which lets grpcurl
// and ghz call the node without a copy of the contract.
//
// It is a development convenience and the caller decides: reflection tells an
// unauthenticated caller every method the node exposes, which outside a
// development profile is a description of the attack surface.
func WithReflection(enabled bool) Option {
	return func(s *settings) { s.reflection = enabled }
}

// WithLogger sets the logger the server itself reports on. It is not the
// logger of the interceptor chain, which receives its own.
func WithLogger(logger *slog.Logger) Option {
	return func(s *settings) { s.logger = logger }
}

// New opens the listener described by cfg and builds the server behind it. The
// context bounds the bind, and nothing beyond it: the server's own lifetime is
// the one given to [Server.Serve].
//
// The caller owns the result and must either [Server.Serve] it or
// [Server.Close] it; a server that is neither leaks the listening socket.
func New(ctx context.Context, cfg *config.Server, opts ...Option) (*Server, error) {
	applied := settings{logger: logging.Discard()}
	for _, opt := range opts {
		opt(&applied)
	}

	var listen net.ListenConfig

	listener, err := listen.Listen(ctx, "tcp", cfg.GRPCAddress)
	if err != nil {
		return nil, errs.Wrapf(err, errs.KindUnavailable,
			"the grpc address %s could not be bound", cfg.GRPCAddress).WithOp(opNew)
	}

	options := make([]grpc.ServerOption, 0, baseServerOptions+len(applied.serverOptions))
	options = append(options,
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepaliveTime,
			Timeout: keepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: minClientPingInterval,
			// A client holding a stream open across a silence is the normal
			// case here, not an abuse of the connection.
			PermitWithoutStream: true,
		}),
		grpc.ChainUnaryInterceptor(applied.unary...),
		grpc.ChainStreamInterceptor(applied.stream...),
	)
	options = append(options, applied.serverOptions...)

	server := grpc.NewServer(options...)
	if applied.reflection {
		reflection.Register(server)
	}

	return &Server{
		server:          server,
		listener:        listener,
		shutdownTimeout: cfg.ShutdownTimeout,
		logger:          applied.logger,
	}, nil
}

// Registrar is where a slice registers its service. Handing out the
// registration surface rather than the server is what keeps a slice from
// reaching for the server's lifecycle.
func (s *Server) Registrar() grpc.ServiceRegistrar { return s.server }

// GetServiceInfo reports the services registered, which is what a metric
// registry needs in order to publish a zero for a method nobody has called
// yet. The name is the one google.golang.org/grpc/reflection.ServiceInfoProvider
// requires, so that the server satisfies it.
func (s *Server) GetServiceInfo() map[string]grpc.ServiceInfo { return s.server.GetServiceInfo() }

// Addr is the address the server is listening on, which is the one a
// configuration of port zero only learns after binding.
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Serve accepts calls until ctx is canceled or the listener fails.
//
// Cancelling ctx stops the server gracefully: the listener closes at once, and
// the calls already running are given the configured shutdown timeout to
// finish. A synchronization stream interrupted mid-batch is not a lost batch —
// the device pushes it again — but it is a round trip over a mobile network
// that the deployment does not have to spend on every rollout.
func (s *Server) Serve(ctx context.Context) error {
	served := make(chan error, 1)

	go func() { served <- s.server.Serve(s.listener) }()

	select {
	case err := <-served:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return errs.Wrap(err, errs.KindUnavailable, "the grpc server stopped accepting calls").WithOp(opServe)
		}

		return nil
	case <-ctx.Done():
		// The shutdown outlives the cancellation that triggered it, which is
		// the whole point of a graceful one.
		s.shutdown(context.WithoutCancel(ctx))
		<-served

		return nil
	}
}

// Close stops the server without waiting for anything, and releases the
// listener. It is safe after Serve has returned, so a caller may defer it.
func (s *Server) Close() { s.server.Stop() }

// shutdown stops the server gracefully, and stops waiting once the shutdown
// timeout has passed. Without the deadline a single stream that never ends —
// which is exactly what a bidirectional sync stream looks like — would hold
// the process open until the orchestrator killed it.
func (s *Server) shutdown(ctx context.Context) {
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		s.server.GracefulStop()
	}()

	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-stopped:
	case <-timer.C:
		s.logger.WarnContext(ctx, "grpc graceful shutdown timed out, closing the calls still running",
			slog.Duration("timeout", s.shutdownTimeout))
		s.server.Stop()
		<-stopped
	}
}
