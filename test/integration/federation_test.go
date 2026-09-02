//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	removeserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/removeserver"
	federationdi "github.com/anthonyvsmuller/quire/internal/federation/di"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	replicarepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/replica"
	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// thePin and theRotatedPin are what a peer publishes as its public key digest,
// before and after its operator rotates the key. Both are in the form C12
// settled on, because a value in any other form is not a pin this node stores.
const (
	thePin        = wellknown.PinPrefix + "Zm9vYmFyCg=="
	theRotatedPin = wellknown.PinPrefix + "YmF6cXV1eAo="
)

// peer is a node serving its own discovery document, which is all this
// instance ever needs from one in order to federate with it.
type peer struct {
	// Domain is the authority a lookup is addressed to.
	Domain string

	mu       sync.Mutex
	endpoint wellknown.ServerEndpoint
}

// newPeer starts a node publishing endpoint. The base URL is filled in with
// the address it actually answers on, since a test cannot know it beforehand.
func newPeer(t *testing.T, endpoint wellknown.ServerEndpoint) *peer {
	t.Helper()

	published := &peer{endpoint: endpoint}

	mux := http.NewServeMux()
	mux.HandleFunc(wellknown.ServerPath, func(w http.ResponseWriter, _ *http.Request) {
		published.mu.Lock()
		document := wellknown.ServerDocument{Server: published.endpoint}
		published.mu.Unlock()

		_ = json.NewEncoder(w).Encode(document)
	})

	listener := httptest.NewServer(mux)
	t.Cleanup(listener.Close)

	published.Domain = strings.TrimPrefix(listener.URL, "http://")

	published.mu.Lock()
	published.endpoint.BaseURL = listener.URL
	published.mu.Unlock()

	return published
}

// rotate republishes the document with a different public key digest, which is
// what an operator deliberately rotating a key does.
func (p *peer) rotate(pin string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.endpoint.CertificateFingerprint = pin
}

// federation is the node's whole gRPC surface, with a reader registered and
// signed in.
type federation struct {
	client quirev1.FederationServiceClient
	// ctx already carries the reader's access token, since every call of this
	// service but none of its use cases needs one.
	ctx context.Context
}

// serveFederation starts the node with both slices registered and returns the
// federation client, authenticated as a reader this node hosts.
func serveFederation(t *testing.T) federation {
	t.Helper()
	reset(t)

	cfg := nodeConfig(t)

	identityContainer, err := identitydi.Initialize(cfg, pool, federationdi.Catalogue(pool), logging.Discard())
	if err != nil {
		t.Fatalf("building the identity slice: %v", err)
	}

	dialer, err := grpcx.NewPeerDialer(&cfg.Federation)
	if err != nil {
		t.Fatalf("building the peer dialer: %v", err)
	}

	t.Cleanup(func() { _ = dialer.Close() })

	federationContainer := federationdi.Initialize(cfg, pool, identityContainer.Migration,
		identityContainer.Users, identityContainer.Devices, dialer)

	grpcServer, err := grpcx.New(t.Context(), &cfg.Server,
		grpcx.WithChain(grpcx.NewChain(logging.Discard())),
		grpcx.WithUnaryInterceptors(identityContainer.Interceptor.Unary()),
		grpcx.WithStreamInterceptors(identityContainer.Interceptor.Stream()),
	)
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}

	identityContainer.Service.Register(grpcServer.Registrar())
	federationContainer.Service.Register(grpcServer.Registrar())

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- grpcServer.Serve(ctx) }()

	connection, err := grpc.NewClient(grpcServer.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing the node: %v", err)
	}

	t.Cleanup(func() {
		_ = connection.Close()

		cancel()

		if err := <-served; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})

	return federation{
		client: quirev1.NewFederationServiceClient(connection),
		ctx:    bearer(t.Context(), signIn(t, quirev1.NewAuthServiceClient(connection))),
	}
}

// signIn registers a reader and returns their access token. Registering is
// also what creates this instance's own row in the catalogue, which every
// reader hosted here references.
func signIn(t *testing.T, client quirev1.AuthServiceClient) string {
	t.Helper()

	if _, err := client.RegisterUser(t.Context(), &quirev1.RegisterUserRequest{
		LocalName:   "anthony",
		DisplayName: "Anthony Muller",
		Email:       "anthony@example.test",
		Password:    thePassword,
	}); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	session, err := client.Login(t.Context(), &quirev1.LoginRequest{
		LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
		Password: thePassword,
		Device:   &quirev1.DeviceBinding{Name: "Pixel 9", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	return session.GetSession().GetAccessToken()
}

// TestFederationRoundTrip walks UC13, UC12 and UC15 in the order a reader
// walks them, against a real database, over a real connection, and against a
// peer that really answers a .well-known lookup.
//
// The subtests share state on purpose and must run in order: what makes this a
// round trip rather than a set of unit tests is that each step starts from what
// the previous one left in the database.
func TestFederationRoundTrip(t *testing.T) {
	node := serveFederation(t)
	other := newPeer(t, wellknown.ServerEndpoint{
		GRPC:                   "quire-b.example:9090",
		JWKSURI:                "https://quire-b.example/.well-known/jwks.json",
		CertificateFingerprint: thePin,
	})

	var known *quirev1.Server

	t.Run("discovery reads what the peer publishes and stores nothing", func(t *testing.T) {
		out, err := node.client.DiscoverServer(node.ctx,
			&quirev1.DiscoverServerRequest{Domain: other.Domain})
		if err != nil {
			t.Fatalf("DiscoverServer: %v", err)
		}

		switch descriptor := out.GetDescriptor_(); {
		case descriptor.GetDomain() != other.Domain:
			t.Errorf("Domain = %q, want the authority the lookup was addressed to", descriptor.GetDomain())
		case descriptor.GetGrpc() != "quire-b.example:9090":
			t.Errorf("Grpc = %q, and D06 is the whole reason the field exists", descriptor.GetGrpc())
		case descriptor.GetCertificateFingerprint() != thePin:
			t.Errorf("CertificateFingerprint = %q", descriptor.GetCertificateFingerprint())
		}

		listed, err := node.client.ListKnownServers(node.ctx, &quirev1.ListKnownServersRequest{})
		if err != nil {
			t.Fatalf("ListKnownServers: %v", err)
		}

		if len(listed.GetServers()) != 1 || !listed.GetServers()[0].GetIsLocal() {
			t.Errorf("the catalogue holds %d rows, want only this instance: a lookup stores nothing",
				len(listed.GetServers()))
		}
	})

	t.Run("adding discovers the peer and records what it found", func(t *testing.T) {
		out, err := node.client.AddKnownServer(node.ctx,
			&quirev1.AddKnownServerRequest{Domain: other.Domain})
		if err != nil {
			t.Fatalf("AddKnownServer: %v", err)
		}

		known = out.GetServer()

		switch {
		case known.GetIsLocal():
			t.Error("a discovered peer was recorded as this instance")
		case !known.GetActive():
			t.Error("a node the reader just added does not take part in replication")
		case known.GetDiscoveredAt() == nil:
			t.Error("the row does not say when its description was learned")
		case known.GetDescriptor_().GetCertificateFingerprint() != thePin:
			t.Error("the pin RNF08 is checked against was not stored")
		}
	})

	t.Run("adding it again is refused rather than pinned again", func(t *testing.T) {
		_, err := node.client.AddKnownServer(node.ctx,
			&quirev1.AddKnownServerRequest{Domain: other.Domain})
		if status.Code(err) != codes.AlreadyExists {
			t.Fatalf("AddKnownServer = %v, want AlreadyExists", err)
		}

		if reason := reasonOf(err); reason != "server_already_known" {
			t.Errorf("reason = %q, want server_already_known", reason)
		}
	})

	t.Run("refreshing a peer that kept its key reports nothing", func(t *testing.T) {
		out, err := node.client.RefreshKnownServer(node.ctx,
			&quirev1.RefreshKnownServerRequest{ServerId: known.GetId()})
		if err != nil {
			t.Fatalf("RefreshKnownServer: %v", err)
		}

		if out.GetCertificateFingerprintChanged() {
			t.Error("an unchanged key was reported as rotated, which is the alarm C12 keeps quiet")
		}
	})

	t.Run("refreshing a peer that rotated its key reports it", func(t *testing.T) {
		other.rotate(theRotatedPin)

		out, err := node.client.RefreshKnownServer(node.ctx,
			&quirev1.RefreshKnownServerRequest{ServerId: known.GetId()})
		if err != nil {
			t.Fatalf("RefreshKnownServer: %v", err)
		}

		if !out.GetCertificateFingerprintChanged() {
			t.Fatal("the reader was not told the node presents a different key, which is theirs to judge")
		}

		if got := out.GetServer().GetDescriptor_().GetCertificateFingerprint(); got != theRotatedPin {
			t.Errorf("the stored pin is %q, and nothing the node presents would match it", got)
		}
	})

	t.Run("authorizing lets the peer hold a copy", func(t *testing.T) {
		out, err := node.client.AuthorizeReplica(node.ctx, &quirev1.AuthorizeReplicaRequest{
			ServerId:        known.GetId(),
			ReplicatesFiles: true,
		})
		if err != nil {
			t.Fatalf("AuthorizeReplica: %v", err)
		}

		authorization := out.GetAuthorization()

		switch {
		case authorization.GetServerDomain() != other.Domain:
			t.Errorf("ServerDomain = %q, and a client would have to ask again to name the node",
				authorization.GetServerDomain())
		case !authorization.GetReplicatesFiles():
			t.Error("the files were left out of a permission that covered them")
		case !authorization.GetActive():
			t.Error("a permission just granted does not stand")
		}
	})

	t.Run("an authorized node is neither forgotten nor stopped", func(t *testing.T) {
		_, err := node.client.RemoveKnownServer(node.ctx,
			&quirev1.RemoveKnownServerRequest{ServerId: known.GetId()})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("RemoveKnownServer = %v, want FailedPrecondition", err)
		}

		if reason := reasonOf(err); reason != "server_in_use" {
			t.Errorf("reason = %q, want server_in_use", reason)
		}

		_, err = node.client.UpdateKnownServer(node.ctx, &quirev1.UpdateKnownServerRequest{
			ServerId:   known.GetId(),
			Server:     &quirev1.Server{Active: false},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"active"}},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("UpdateKnownServer = %v, want FailedPrecondition", err)
		}
	})

	t.Run("this instance is never forgotten", func(t *testing.T) {
		listed, err := node.client.ListKnownServers(node.ctx, &quirev1.ListKnownServersRequest{})
		if err != nil {
			t.Fatalf("ListKnownServers: %v", err)
		}

		var local string

		for _, entry := range listed.GetServers() {
			if entry.GetIsLocal() {
				local = entry.GetId()
			}
		}

		if local == "" {
			t.Fatal("the catalogue does not hold this instance, which every reader here references")
		}

		_, err = node.client.RemoveKnownServer(node.ctx,
			&quirev1.RemoveKnownServerRequest{ServerId: local})
		if reason := reasonOf(err); reason != "local_server" {
			t.Errorf("RemoveKnownServer for this instance = %v, want local_server", err)
		}
	})

	t.Run("revoking keeps the row that explains the copy", func(t *testing.T) {
		if _, err := node.client.RevokeReplica(node.ctx,
			&quirev1.RevokeReplicaRequest{ServerId: known.GetId()}); err != nil {
			t.Fatalf("RevokeReplica: %v", err)
		}

		standing, err := node.client.ListReplicaAuthorizations(node.ctx,
			&quirev1.ListReplicaAuthorizationsRequest{})
		if err != nil {
			t.Fatalf("ListReplicaAuthorizations: %v", err)
		}

		if len(standing.GetAuthorizations()) != 0 {
			t.Errorf("a withdrawn permission is still shown by default: %+v", standing.GetAuthorizations())
		}

		everything, err := node.client.ListReplicaAuthorizations(node.ctx,
			&quirev1.ListReplicaAuthorizationsRequest{IncludeInactive: true})
		if err != nil {
			t.Fatalf("ListReplicaAuthorizations: %v", err)
		}

		if len(everything.GetAuthorizations()) != 1 || everything.GetAuthorizations()[0].GetActive() {
			t.Errorf("the row that explains a peer still holding data is gone: %+v",
				everything.GetAuthorizations())
		}
	})

	t.Run("the peer is forgotten once nobody authorizes it", func(t *testing.T) {
		if _, err := node.client.RemoveKnownServer(node.ctx,
			&quirev1.RemoveKnownServerRequest{ServerId: known.GetId()}); err != nil {
			t.Fatalf("RemoveKnownServer: %v", err)
		}

		_, err := node.client.GetKnownServer(node.ctx,
			&quirev1.GetKnownServerRequest{ServerId: known.GetId()})
		if status.Code(err) != codes.NotFound {
			t.Errorf("GetKnownServer = %v, want NotFound", err)
		}
	})
}

// TestDiscoveringAHostThatIsNotANode covers the two failures a reader has to
// be able to tell apart, over a real network stack: a host that does not
// answer is worth retrying, and one that answers with something else is not.
func TestDiscoveringAHostThatIsNotANode(t *testing.T) {
	node := serveFederation(t)

	silent := httptest.NewServer(http.NewServeMux())
	address := strings.TrimPrefix(silent.URL, "http://")
	silent.Close()

	_, err := node.client.DiscoverServer(node.ctx, &quirev1.DiscoverServerRequest{Domain: address})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("DiscoverServer against a closed listener = %v, want Unavailable", err)
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>Sign in</title>"))
	}))
	t.Cleanup(page.Close)

	_, err = node.client.DiscoverServer(node.ctx,
		&quirev1.DiscoverServerRequest{Domain: strings.TrimPrefix(page.URL, "http://")})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("DiscoverServer against a host that is not a node = %v, want FailedPrecondition", err)
	}

	if reason := reasonOf(err); reason != "not_a_quire_server" {
		t.Errorf("reason = %q, want not_a_quire_server", reason)
	}
}

// TestTheCatalogueStatements covers what a unit test cannot: the constraints
// the fakes in internal/federation/application/apptest only imitate.
func TestTheCatalogueStatements(t *testing.T) {
	reset(t)

	cfg := nodeConfig(t)
	manager := persist.NewManager(pool)
	servers := serverrepository.New(manager)

	local := &server.Descriptor{
		Domain:        server.ParseDomain(cfg.Server.Name),
		BaseURL:       server.BaseURL(cfg.Server.BaseURL.String()),
		GRPCAuthority: server.GRPCAuthority(cfg.Server.GRPCAdvertisedAddress),
	}

	t.Run("this instance is written once and found afterwards", func(t *testing.T) {
		first, err := servers.EnsureLocal(t.Context(), local)
		if err != nil {
			t.Fatalf("the first resolution: %v", err)
		}

		second, err := servers.EnsureLocal(t.Context(), local)
		if err != nil {
			t.Fatalf("the second resolution: %v", err)
		}

		if first.ID != second.ID {
			t.Errorf("the node has two identities, %s and %s, and its readers point at one of them",
				first.ID, second.ID)
		}

		if second.GRPCAuthority != local.GRPCAuthority {
			t.Errorf("GRPCAuthority = %q, want the address this node advertises", second.GRPCAuthority)
		}

		if !second.IsLocal {
			t.Error("the row that is this instance came back as a peer")
		}
	})

	t.Run("a second instance collides with the partial unique index", func(t *testing.T) {
		renamed := *local
		renamed.Domain = "quire-renamed.example"

		if _, err := servers.EnsureLocal(t.Context(), &renamed); err == nil {
			t.Fatal("EnsureLocal under a second domain = nil, want the index to refuse it")
		}

		var claiming int
		if err := pool.QueryRow(t.Context(),
			"SELECT count(*) FROM federation.servers WHERE is_local").Scan(&claiming); err != nil {
			t.Fatalf("counting the local rows: %v", err)
		}

		if claiming != 1 {
			t.Errorf("%d rows claim to be this instance, and «is this reader local» is unanswerable",
				claiming)
		}
	})

	t.Run("one row per domain", func(t *testing.T) {
		first, err := server.New(peerDescriptor("quire-b.example"), time.Now().UTC())
		if err != nil {
			t.Fatalf("server.New: %v", err)
		}

		if created := servers.Create(t.Context(), first); created != nil {
			t.Fatalf("Create: %v", created)
		}

		second, err := server.New(peerDescriptor("quire-b.example"), time.Now().UTC())
		if err != nil {
			t.Fatalf("server.New: %v", err)
		}

		err = servers.Create(t.Context(), second)
		if code := errs.CodeOf(err); code != server.CodeDomainKnown {
			t.Errorf("Create = %v (code %q), want %q", err, code, server.CodeDomainKnown)
		}
	})

	t.Run("an authority without a port is refused by the database too", func(t *testing.T) {
		// Built with Restore rather than New, which is the only way to get a
		// value the domain would have rejected as far as the statement. The
		// check constraint is the second half of the same rule, and it is what
		// holds if anything ever writes this table without going through the
		// entity.
		malformed := server.Restore(uuid.New(), &server.Props{
			Descriptor: server.Descriptor{
				Domain:        "quire-c.example",
				BaseURL:       "https://quire-c.example",
				GRPCAuthority: "quire-c.example",
			},
			DiscoveredAt: time.Now().UTC(),
			Active:       true,
		})

		if err := servers.Create(t.Context(), malformed); err == nil {
			t.Error("Create with a portless authority = nil, want servers_grpc_authority_format to refuse it")
		}
	})
}

// TestForgettingANodeSerializesWithAuthorizingIt is the race the row lock
// exists for, run against a real database.
//
// Forgetting a node reads the authorizations and then deletes the row, and
// under READ COMMITTED a statement sees the snapshot it began with — so a
// permission granted in between would be invisible to the check, and the
// foreign key cascades, so it would be deleted rather than refuse the delete.
// Both calls take the catalogue row with SELECT ... FOR UPDATE, so the second
// waits and then sees what the first committed.
//
// The test holds the lock in one transaction, waits until PostgreSQL reports
// the other session blocked on it, and only then commits — so the interleaving
// it is about is the one that actually happens.
func TestForgettingANodeSerializesWithAuthorizingIt(t *testing.T) {
	repositories := newRepositories(t)
	reader := repositories.reader(t, "anthony", "anthony@example.test")

	servers := serverrepository.New(repositories.manager)
	replicas := replicarepository.New(repositories.manager)

	peerNode, err := server.New(peerDescriptor("quire-b.example"), time.Now().UTC())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if created := servers.Create(t.Context(), peerNode); created != nil {
		t.Fatalf("Create: %v", created)
	}

	held := make(chan struct{})
	granting := make(chan error, 1)

	go func() {
		granting <- repositories.manager.Within(t.Context(), func(ctx context.Context) error {
			if _, locked := servers.GetByIDForUpdate(ctx, peerNode.ID); locked != nil {
				return locked
			}

			close(held)

			// Blocks until the removal below is waiting on this row, so that
			// the authorization is written while that removal is mid-flight
			// rather than before it started.
			waitForABlockedSession(ctx, t)

			authorization, granted := replica.New(reader.ID, peerNode.ID, true, time.Now().UTC())
			if granted != nil {
				return granted
			}

			return replicas.Create(ctx, authorization)
		})
	}()

	<-held

	// The use case and not the repository: the lock is taken by the call, and
	// a test that deleted through the statement alone would be testing the
	// half of the rule that cannot hold on its own.
	_, err = removeserverusecase.New(servers, replicas, repositories.manager).
		Execute(t.Context(), removeserverusecase.Input{ServerID: peerNode.ID})

	if grantErr := <-granting; grantErr != nil {
		t.Fatalf("granting the authorization: %v", grantErr)
	}

	if code := errs.CodeOf(err); code != server.CodeServerInUse {
		t.Fatalf("Execute = %v (code %q), want %q: the node was forgotten, and the authorization "+
			"granted a moment earlier went with it", err, code, server.CodeServerInUse)
	}

	if _, err := servers.GetByID(t.Context(), peerNode.ID); err != nil {
		t.Fatalf("the node is gone from the catalogue: %v", err)
	}

	var surviving int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM federation.user_replicas WHERE active").Scan(&surviving); err != nil {
		t.Fatalf("counting the authorizations: %v", err)
	}

	if surviving != 1 {
		t.Errorf("%d authorizations survive, and RN03 is the promise the reader can still revoke one",
			surviving)
	}
}

// waitForABlockedSession blocks until another session of this database is
// waiting on a lock, which is how the test knows the removal has reached the
// row rather than merely been started.
func waitForABlockedSession(ctx context.Context, t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		var waiting int

		err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		if err != nil {
			t.Errorf("reading pg_stat_activity: %v", err)

			return
		}

		if waiting > 0 {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Error("no session ever blocked on the catalogue row, so the lock is not doing anything")
}

// peerDescriptor is a node as a well-formed discovery document describes it.
func peerDescriptor(domain server.Domain) *server.Descriptor {
	return &server.Descriptor{
		Domain:                 domain,
		BaseURL:                server.BaseURL("https://" + domain.String()),
		JWKSURI:                server.JWKSURI("https://" + domain.String() + "/.well-known/jwks.json"),
		CertificateFingerprint: thePin,
		GRPCAuthority:          server.GRPCAuthority(domain.String() + ":9090"),
	}
}
