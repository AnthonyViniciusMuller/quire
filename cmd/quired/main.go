// Command quired is the Quire node: one gRPC server for the API and one HTTP
// server for discovery, the signing keys, health and metrics, started and
// stopped together.
//
// Everything the process needs is read from the environment once, at startup,
// and handed down. Nothing below this file reads a variable, opens a listener
// or decides a timeout — which is what makes every other package testable
// without an environment, and what makes the whole configuration surface of
// the node enumerable by reading one struct.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	federationdi "github.com/anthonyvsmuller/quire/internal/federation/di"
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/jwks"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/metrics"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

func main() {
	if err := run(context.Background()); err != nil {
		// The logger may not exist yet — a node that cannot read its
		// configuration cannot build one — so the last word goes to stderr.
		fmt.Fprintf(os.Stderr, "quired: %v\n", err)
		os.Exit(1)
	}
}

// run starts the node and returns when it has stopped, or when it could not
// start.
func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Log, os.Stdout)

	// Whatever logs through the package-level slog — a dependency, a corner of
	// the standard library — joins the same stream rather than writing plain
	// text beside it.
	slog.SetDefault(logger)

	// SIGTERM is how an orchestrator asks for the graceful shutdown both
	// servers implement; SIGINT is the same request from a terminal.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := persist.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}

	defer pool.Close()

	registry, err := metrics.New()
	if err != nil {
		return err
	}

	// The federation slice comes first because the identity slice needs the
	// catalogue from it: a reader is bound to the node that hosts them (UC14),
	// and the row that says which node this is lives in federation.servers.
	// Wiring the two is this file's job — neither slice imports the other's
	// adapters.
	federation := federationdi.Initialize(pool)

	// Built before the listeners, so that a deployment fault — a signing key
	// the node cannot read, a hashing cost bcrypt refuses, no way to deliver a
	// password recovery — stops the node while it is still starting rather than
	// at the first call that needs it.
	identity, err := identitydi.Initialize(cfg, pool, federation.Servers)
	if err != nil {
		return err
	}

	grpcServer, err := grpcx.New(ctx, &cfg.Server,
		grpcx.WithChain(grpcx.NewChain(logger).Around(registry.GRPCServerInterceptors())),
		// Nearest the handler, and after the chain above on purpose. A call it
		// rejects is still counted, still carries the request identifier, is
		// still logged, and is still translated into a status by the
		// interceptors it sits inside — and a panic in it is recovered like any
		// other, which it would not be if it sat outside recovery.
		grpcx.WithUnaryInterceptors(identity.Interceptor.Unary()),
		grpcx.WithStreamInterceptors(identity.Interceptor.Stream()),
		// Reflection tells an unauthenticated caller every method the node
		// exposes, which is a convenience while developing and a description
		// of the attack surface in production.
		grpcx.WithReflection(!cfg.Environment.IsProduction()),
		grpcx.WithLogger(logger),
	)
	if err != nil {
		return err
	}

	defer grpcServer.Close()

	identity.Service.Register(grpcServer.Registrar())

	// After every service is registered, so that a method nobody has called
	// yet still has a series rather than appearing the first time it fails.
	registry.InitializeGRPC(grpcServer)

	httpServer, err := newHTTPServer(ctx, cfg, logger, registry, identity.Auth, pool.Ping)
	if err != nil {
		return err
	}

	defer httpServer.Close()

	logger.InfoContext(ctx, "the node is serving",
		slog.String("server_name", cfg.Server.Name),
		slog.String("environment", string(cfg.Environment)),
		slog.String("grpc_address", grpcServer.Addr().String()),
		slog.String("http_address", httpServer.Addr().String()))

	// The group's context is what ties the two together: whichever server
	// fails first cancels the other, so the node never keeps answering health
	// checks with half of itself gone.
	group, serving := errgroup.WithContext(ctx)
	group.Go(func() error { return grpcServer.Serve(serving) })
	group.Go(func() error { return httpServer.Serve(serving) })

	if err := group.Wait(); err != nil {
		return err
	}

	logger.InfoContext(ctx, "the node stopped")

	return nil
}

// newHTTPServer builds the listener that serves discovery, the signing keys,
// health and metrics.
func newHTTPServer(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	registry *metrics.Registry,
	authService service.AuthService,
	databaseReady httpx.Probe,
) (*httpx.Server, error) {
	options, err := wellknown.Serve(cfg)
	if err != nil {
		return nil, err
	}

	options = append(options,
		httpx.WithLogger(logger),
		httpx.WithMetrics(registry.Handler()),
		// The public half of the signing key, at the path the discovery
		// document above points at (RNF11).
		jwks.Serve(authService),
		// Readiness, never liveness: a database that stopped answering must
		// take this node out of rotation, not have it restarted in a loop.
		httpx.WithReadinessProbe("database", databaseReady),
	)

	return httpx.New(ctx, &cfg.Server, options...)
}
