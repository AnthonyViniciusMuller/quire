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
	"google.golang.org/grpc"

	federationdi "github.com/anthonyvsmuller/quire/internal/federation/di"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/peerauthn"
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/jwks"
	librarydi "github.com/anthonyvsmuller/quire/internal/library/di"
	readingdi "github.com/anthonyvsmuller/quire/internal/reading/di"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/metrics"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
	syncdi "github.com/anthonyvsmuller/quire/internal/sync/di"
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

	// One clock for the node, and every slice that stamps a replication
	// timestamp shares it. C01 makes that instant causally monotonic rather
	// than a reading of the machine's clock, and a second clock in this
	// process would be a second answer to what "after" means here.
	clock := hlc.New()

	// The two slices need each other, and the catalogue is what breaks the
	// knot. The identity slice binds a reader to the node that hosts them
	// (UC14) and needs the catalogue to do it; the federation slice serves
	// UC16, whose controller the identity slice holds because the work is an
	// account, its devices and a session. Wiring the two is this file's job —
	// neither slice imports the other's container.
	catalogue := federationdi.Catalogue(pool)

	// Built before the listeners, so that a deployment fault — a signing key
	// the node cannot read, a hashing cost bcrypt refuses, no way to deliver a
	// password recovery — stops the node while it is still starting rather than
	// at the first call that needs it.
	identity, err := identitydi.Initialize(cfg, pool, catalogue, logger)
	if err != nil {
		return err
	}

	// The one client this node is of other nodes, shared by the slices that
	// call them. It can fail for one reason, this node's own certificate: a
	// key pair the process cannot read is a deployment fault, and a node that
	// discovered it at the first replication tick would have started,
	// answered every device, and quietly replicated to nobody.
	dialer, err := grpcx.NewPeerDialer(&cfg.Federation)
	if err != nil {
		return err
	}

	defer func() { _ = dialer.Close() }()

	federation := federationdi.Initialize(cfg, pool, identity.Migration, identity.Users, identity.Devices)

	// For the same reason, and one of its own: which object store holds the
	// readers' files is decided here, and an endpoint the SDK cannot address
	// or a service account key the node cannot read is a deployment fault. A
	// node that cannot store a file cannot serve UC02 at all, and should say
	// so before it starts answering.
	library, err := librarydi.Initialize(ctx, cfg, pool, clock, logger)
	if err != nil {
		return err
	}

	defer func() { _ = library.Close() }()

	// After the library slice, because it reads through it: both of the
	// reading slice's tables reference a work and neither references a reader,
	// so whose a mark or a position is is established through the library's
	// works repository. Wiring the two is this file's job — neither slice
	// imports the other's container.
	reading := readingdi.Initialize(pool, library.Ebooks, clock)

	// Last, because it writes through both of them: the records that replicate
	// belong to the library and reading slices, and the reconciler reaches
	// every one of them through the repository its own slice declares. Wiring
	// that is this file's job — the sync slice imports no container but its
	// own.
	synchronization := syncdi.Initialize(cfg, pool, clock, dialer,
		federation.Servers, federation.Authorizations, &syncdi.Records{
			Works:     library.Ebooks,
			Groupings: library.Collections,
			Filings:   library.Memberships,
			Marks:     reading.Annotations,
			Positions: reading.Progress,
		}, logger)

	// What the listener presents, and nil in a deployment with no certificate
	// of its own. It serves devices and peers alike: a device authenticates
	// with a token and carries no certificate, and a peer is identified by the
	// one it does — which is why the client certificate is requested and never
	// required.
	peerCredentials, err := peerauthn.ServerCredentials(&cfg.Federation)
	if err != nil {
		return err
	}

	serverOptions := make([]grpc.ServerOption, 0, 1)
	if peerCredentials != nil {
		serverOptions = append(serverOptions, grpc.Creds(peerCredentials))
	}

	grpcServer, err := grpcx.New(ctx, &cfg.Server,
		grpcx.WithServerOptions(serverOptions...),
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
	federation.Service.Register(grpcServer.Registrar())
	library.Service.Register(grpcServer.Registrar())
	reading.Service.Register(grpcServer.Registrar())
	synchronization.Service.Register(grpcServer.Registrar())

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
	// The third member of the group is not a server. Replication is driven
	// from the side that owes the data, so nobody calls it and nothing would
	// start it — and it stops with the two listeners because a node that kept
	// replicating after it stopped answering would be half a node.
	group.Go(func() error { return synchronization.Worker.Run(serving) })
	// Nor is the fourth. A password recovery is handed to this worker instead
	// of being delivered on the call that asked for one, so that the call takes
	// the same time whether or not the address is registered here — C13 in
	// docs/tcc-corrections.md, and the whole of why the delivery is not simply
	// awaited where it is requested.
	group.Go(func() error { return identity.Deliveries.Run(serving) })
	// Nor is the fifth. A chunked upload leaves the node holding a
	// half-received file between calls — D11 in docs/tcc-corrections.md, and
	// the second reason this deployment runs a single replica — and a client
	// that stopped sending is one nobody will ever hear from again. This ends
	// the sessions nobody is sending to, and releases every one still open when
	// the node stops.
	group.Go(func() error { return library.Uploads.Run(serving) })

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
