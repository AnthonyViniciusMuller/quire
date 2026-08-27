// Command quired is the Quire node: one gRPC server for the API and one HTTP
// server for discovery, health and metrics, started and stopped together.
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

	grpcServer, err := grpcx.New(ctx, &cfg.Server,
		grpcx.WithChain(grpcx.NewChain(logger).Around(registry.GRPCServerInterceptors())),
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

	// After every service is registered, so that a method nobody has called
	// yet still has a series. There are none yet: the slices register theirs
	// from phase 5 on, and until then the node serves only what is below.
	registry.InitializeGRPC(grpcServer)

	httpServer, err := newHTTPServer(ctx, cfg, logger, registry, pool.Ping)
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

// newHTTPServer builds the listener that serves discovery, health and metrics.
func newHTTPServer(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	registry *metrics.Registry,
	databaseReady httpx.Probe,
) (*httpx.Server, error) {
	options, err := wellknown.Serve(cfg)
	if err != nil {
		return nil, err
	}

	options = append(options,
		httpx.WithLogger(logger),
		httpx.WithMetrics(registry.Handler()),
		// Readiness, never liveness: a database that stopped answering must
		// take this node out of rotation, not have it restarted in a loop.
		httpx.WithReadinessProbe("database", databaseReady),
	)

	return httpx.New(ctx, &cfg.Server, options...)
}
