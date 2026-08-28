// Package di builds the identity slice: it constructs every adapter, wires them
// into the use cases, wires those into the controllers, and hands back the
// three things the node needs from this slice.
//
// It is the only place where a concrete adapter is named. Everything above it
// holds a port, so substituting bcrypt for another hashing algorithm is a
// change to a constructor here and to nothing else.
//
// The one port it does not build an adapter for is the catalogue: that table
// belongs to the federation slice, so the node hands this one the repository
// and the resolver of UC14 is wired over it. What this slice depends on is the
// port in that slice's domain, never an adapter of it.
//
// It reads no environment variable and opens no connection. The configuration
// arrives loaded and the pool arrives open, because both are shared with the
// slices that follow and neither is this slice's to decide.
package di

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/migratehomeserver"
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	changepasswordusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/changepassword"
	deleteuserusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/deleteuser"
	getuserusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/getuser"
	listdevicesusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/listdevices"
	loginusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	logoutusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/logout"
	migratehomeserverusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/migratehomeserver"
	refreshusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/refresh"
	registerusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	registerdeviceusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/registerdevice"
	requestrecoveryusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/requestrecovery"
	resetpasswordusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/resetpassword"
	revokedeviceusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/revokedevice"
	updatedeviceusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updatedevice"
	updateuserusecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updateuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authservice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/changepassword"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/deleteuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/getuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/listdevices"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/login"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/logout"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/refreshsession"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/registerdevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/registeruser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/requestpasswordrecovery"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/resetpassword"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/revokedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/updatedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/controller/updateuser"
	credentialrepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/credential"
	devicerepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/device"
	userrepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/user"
	clockservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/clock"
	deferredservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/deferred"
	hashservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/hash"
	localserverservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/localserver"
	mailerservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/mailer"
	smtpservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/smtp"
	tokenservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/token"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// Container is what the node takes from this slice.
type Container struct {
	// Auth issues and verifies the credentials of RNF11. The node needs it
	// beyond this slice: the JWKS endpoint publishes the public half of the
	// key it signs with.
	Auth service.AuthService
	// Interceptor authenticates every call the node serves, not only this
	// slice's — it is the only component that can verify a token, and the
	// methods of the slices to come are authenticated by default.
	Interceptor *authn.Interceptor
	// Service is the gRPC surface of the slice, ready to be registered.
	Service *authservice.Service

	// Deliveries is the worker that delivers the password recoveries this
	// slice issues. It is here for the reason the replication worker is in the
	// sync slice's container: nobody calls it, so nothing would start it, and
	// the node is what starts the things that run on their own.
	Deliveries *deferredservice.Service

	// Migration serves FederationService.MigrateHomeServer (UC16, RF17).
	//
	// It is this slice's controller and the federation slice's method, because
	// what UC16 changes is which node a reader belongs to and what it writes is
	// an account, its devices and a session — and only this slice holds any of
	// those. The node hands it over when it builds the federation container,
	// which is why that one is built after this one.
	Migration *migratehomeserver.MigrateHomeServer
}

// Initialize builds the slice over the node's configuration and connection
// pool.
//
// It fails rather than degrades. A signing key the node cannot read, a hashing
// cost bcrypt refuses, or a deployment with no way to deliver a password
// recovery are all deployment faults, and each of them is better as a node that
// does not start than as a call that fails once somebody depends on it.
func Initialize(
	cfg *config.Config, pool *pgxpool.Pool, servers server.Repository, logger *slog.Logger,
) (*Container, error) {
	manager := persist.NewManager(pool)

	users := userrepository.New(manager)
	devices := devicerepository.New(manager)
	credentials := credentialrepository.New(manager)

	hasher, err := hashservice.New(cfg.Auth.BcryptCost)
	if err != nil {
		return nil, err
	}

	auth, err := tokenservice.New(&cfg.Auth, cfg.Server.Name)
	if err != nil {
		return nil, err
	}

	transport, err := mailer(cfg)
	if err != nil {
		return nil, err
	}

	// The transport is reached through the queue and never directly. C13 in
	// docs/tcc-corrections.md names both as missing, and the queue is what
	// makes RequestPasswordRecovery take the same time for an address that
	// exists as for one that does not — which the uniform reply on its own
	// cannot do.
	notifier := deferredservice.New(transport, &cfg.Mail, logger)

	clock := clockservice.New()

	localServer, err := localserverservice.New(servers, &cfg.Server)
	if err != nil {
		return nil, err
	}

	// The manager itself is the unit of work: its Within is the port, so no
	// adapter stands between them.
	transaction := manager

	controllers := authservice.Controllers{
		RegisterUser: registeruser.New(registerusecase.New(users, hasher, localServer, clock)),
		GetUser:      getuser.New(getuserusecase.New(users, localServer)),
		UpdateUser:   updateuser.New(updateuserusecase.New(users, localServer, clock)),
		ChangePassword: changepassword.New(
			changepasswordusecase.New(users, credentials, hasher, clock, transaction)),
		DeleteUser: deleteuser.New(deleteuserusecase.New(users, hasher)),
		Login: login.New(
			loginusecase.New(users, devices, credentials, hasher, auth, localServer, clock, transaction)),
		Logout:         logout.New(logoutusecase.New(credentials, auth)),
		RefreshSession: refreshsession.New(refreshusecase.New(credentials, devices, auth, clock, transaction)),
		RequestPasswordRecovery: requestpasswordrecovery.New(
			requestrecoveryusecase.New(users, credentials, auth, notifier, localServer, clock, transaction)),
		ResetPassword: resetpassword.New(
			resetpasswordusecase.New(users, credentials, hasher, auth, clock, transaction)),
		RegisterDevice: registerdevice.New(registerdeviceusecase.New(devices)),
		ListDevices:    listdevices.New(listdevicesusecase.New(devices)),
		UpdateDevice:   updatedevice.New(updatedeviceusecase.New(devices)),
		RevokeDevice:   revokedevice.New(revokedeviceusecase.New(devices, credentials, transaction)),
	}

	migration := migratehomeserverusecase.New(
		users, devices, credentials, hasher, auth, localServer, clock, transaction)

	return &Container{
		Auth:        auth,
		Interceptor: authn.New(auth, clock, authn.PublicMethods()),
		Service:     authservice.New(&controllers),
		Deliveries:  notifier,
		Migration:   migratehomeserver.New(migration),
	}, nil
}

// mailer builds the adapter that delivers a password recovery, chosen by which
// section of the configuration the deployment filled in — never by a variable
// naming the transport, for the reason config.Mail.Transport gives.
//
// The default case is the same as the empty one on purpose. A transport this
// function has not been taught about is a deployment that asked for a delivery
// this node cannot make, and the adapter below is the one that says so and
// refuses to be built in production — which is a better answer than a node
// that starts and silently delivers nowhere.
func mailer(cfg *config.Config) (service.Mailer, error) {
	switch cfg.Mail.Transport() {
	case config.MailTransportSMTP:
		return smtpservice.New(&cfg.Mail, cfg.Server.Name)

	case config.MailTransportNone:
		return mailerservice.New(cfg.Environment)

	default:
		return mailerservice.New(cfg.Environment)
	}
}
