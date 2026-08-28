//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/client"

	federationreplica "github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	replicarepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/replica"
	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
	identitydevice "github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	identityuser "github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	devicerepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/device"
	userrepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/user"
	ebookrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// UC15 and RF16, over the whole path: a reader tells their node about another
// one, allows it to hold a copy, and the changes they make afterwards arrive
// there without anybody asking for them.
//
// Everything between the two nodes is what the federation actually does. The
// origin discovers the peer over RFC 8615 and pins the public key it published
// (C12); the delivery queue is filled from the log rather than by the call that
// wrote the change, which is what makes a peer authorized today and a peer that
// missed a week the same case; and the connection is mTLS, checked against that
// pin on both ends rather than against an authority neither of them shares.
//
// One thing in the middle is not the federation, and it is the point of C22:
// the peer has to already hold the reader's row and the reader's permission
// before it may be replicated to, and nothing in the contract can tell it
// either. What [admit] does is what the missing call would do, out of the
// document the origin already publishes, and no more.
func TestAnAuthorizedNodeIsSentTheReadersChanges(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	peer := addKnownServer(t, tablet, nodeB.domain)

	if _, err := tablet.AuthorizeReplica(t.Context(), peer, true); err != nil {
		t.Fatalf("authorizing %s to replicate: %v", nodeB.domain, err)
	}

	admit(t, nodeB, nodeA, tablet)

	// Authored offline, because a change made through the connected path
	// reaches the log through nothing at all (C21) and the queue is filled
	// from the log.
	tablet.disconnect(t)
	work := createWork(t, tablet, "Grande Sertão: Veredas")
	tablet.reconnect(t)
	push(t, tablet)

	works := ebookrepository.New(persist.NewManager(connect(t, nodeB)))

	eventually(t, "the change to reach "+nodeB.domain, func() bool {
		held, err := works.GetByID(t.Context(), work)
		if err != nil {
			if !errors.Is(err, errs.KindNotFound) {
				t.Fatalf("reading the work on %s: %v", nodeB.domain, err)
			}

			return false
		}

		return held.Title.String() == "Grande Sertão: Veredas"
	})
}

// RN03, from the other side: a node the reader never allowed is a node that
// gets nothing, and it does not learn whether the reader it was told about
// exists.
//
// The refusal is the same words for a reader who is not here and a reader who
// never authorized this node, which is what stops an authorization for one
// reader from being an oracle for the rest.
func TestAnUnauthorizedNodeIsRefused(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	peer := addKnownServer(t, tablet, nodeB.domain)

	if _, err := tablet.AuthorizeReplica(t.Context(), peer, false); err != nil {
		t.Fatalf("authorizing %s to replicate: %v", nodeB.domain, err)
	}

	// Node B is told about node A and about nobody: the reader's row and the
	// permission are exactly what is left out.
	admitPeer(t, nodeB, nodeA)

	tablet.disconnect(t)
	work := createWork(t, tablet, "Sagarana")
	tablet.reconnect(t)
	push(t, tablet)

	works := ebookrepository.New(persist.NewManager(connect(t, nodeB)))

	// Long enough for several passes of the replication worker, which is what
	// makes this an observation rather than a race won by the assertion.
	time.Sleep(settleFor / 3)

	if _, err := works.GetByID(t.Context(), work); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("%s holds a work for a reader who never authorized it: %v", nodeB.domain, err)
	}
}

// The pin is the whole of the trust between two nodes (RNF08, C12), so a node
// whose key is not the one the catalogue recorded is a node this one will not
// talk to — and that has to hold against a peer that answers perfectly well.
func TestAPeerIsRefusedWhenItsKeyIsNotTheOnePinned(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	peer := addKnownServer(t, tablet, nodeB.domain)

	if _, err := tablet.AuthorizeReplica(t.Context(), peer, false); err != nil {
		t.Fatalf("authorizing %s to replicate: %v", nodeB.domain, err)
	}

	admit(t, nodeB, nodeA, tablet)
	repin(t, nodeA, peer, wellknown.PinPrefix+"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")

	tablet.disconnect(t)
	work := createWork(t, tablet, "Tutaméia")
	tablet.reconnect(t)
	push(t, tablet)

	works := ebookrepository.New(persist.NewManager(connect(t, nodeB)))

	time.Sleep(settleFor / 3)

	if _, err := works.GetByID(t.Context(), work); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("%s was sent a change over a certificate the catalogue does not name: %v",
			nodeB.domain, err)
	}
}

// addKnownServer records a node in the catalogue and returns its identifier
// (UC12).
//
// It is the ordinary call, and it is what pins the peer: only the domain is
// sent, and everything else in the record is what the peer said about itself.
//
// A node already recorded is read rather than added again, and that is C15
// rather than a convenience: the catalogue is node-wide, so the second reader
// of this suite to reach for the same peer finds it already there. Every reader
// then shares one pinned key, which is the property the correction is about and
// which this suite would otherwise trip over on its second test.
func addKnownServer(t *testing.T, appliance *device, domain string) uuid.UUID {
	t.Helper()

	recorded, err := appliance.AddKnownServer(t.Context(), domain)

	if status.Code(err) == codes.AlreadyExists {
		return knownServer(t, appliance, domain)
	}

	if err != nil {
		t.Fatalf("recording %s on %s: %v", domain, appliance.on.domain, err)
	}

	if recorded.GetDescriptor_().GetCertificateFingerprint() == "" {
		t.Fatalf("%s published no pin, so nothing can be replicated to it", domain)
	}

	return uuid.MustParse(recorded.GetId())
}

// knownServer finds a node the catalogue already holds.
func knownServer(t *testing.T, appliance *device, domain string) uuid.UUID {
	t.Helper()

	known, err := appliance.ListKnownServers(t.Context(), true)
	if err != nil {
		t.Fatalf("reading the catalogue of %s: %v", appliance.on.domain, err)
	}

	for _, node := range known {
		if node.GetDescriptor_().GetDomain() == domain {
			return uuid.MustParse(node.GetId())
		}
	}

	t.Fatalf("%s is in the catalogue of %s and is not in its listing", domain, appliance.on.domain)

	return uuid.UUID{}
}

// admit puts on `to` everything it has to hold before `from` may replicate a
// reader to it: the peer's row with the peer's pin, the reader's own row, and
// the permission.
//
// This is C22 in docs/tcc-corrections.md, and it is worth being precise about
// what it stands in for. UC15 is served entirely by the origin — the reader
// authorizes a node, the origin records it and starts offering — and the
// destination refuses everything until its own catalogue names the origin and
// its own tables name the reader (RN03, checked there as well). Nothing in the
// contract lets one node tell another any of that, so a federation assembled
// only through the API cannot replicate at all.
//
// What is written here is exactly what the missing call would carry, and it is
// read out of the discovery document the origin already publishes rather than
// assembled by hand: the fields exist, they are already public, and what is
// absent is a call that hands them over with the reader's permission attached.
func admit(t *testing.T, to, from *node, appliance *device) {
	t.Helper()

	origin := admitPeer(t, to, from)
	manager := persist.NewManager(connect(t, to))

	state := appliance.State()
	userID := state.User.ID

	localName, err := identityuser.ParseLocalName(state.User.LocalName)
	if err != nil {
		t.Fatalf("the reader's name: %v", err)
	}

	displayName, err := identityuser.ParseDisplayName("A reader replicated from " + from.domain)
	if err != nil {
		t.Fatalf("the reader's display name: %v", err)
	}

	now := time.Now().UTC()

	// No address and no password digest, which is what makes this a
	// replicated reader rather than one this node authenticates (C03): the
	// row exists so that what they wrote has somewhere to hang, and RN08
	// leaves authenticating them to the node that hosts them.
	replicated := identityuser.Restore(userID, &identityuser.Props{
		OriginServerID: origin,
		LocalName:      localName,
		DisplayName:    displayName,
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	if err = userrepository.New(manager).Create(t.Context(), replicated); err != nil &&
		!errors.Is(err, errs.KindAlreadyExists) {
		t.Fatalf("recording the reader on %s: %v", to.domain, err)
	}

	permission, err := federationreplica.New(userID, origin, true, now)
	if err != nil {
		t.Fatalf("granting %s the permission to send: %v", from.domain, err)
	}

	if err = replicarepository.New(manager).Create(t.Context(), permission); err != nil &&
		!errors.Is(err, errs.KindAlreadyExists) {
		t.Fatalf("recording the permission on %s: %v", to.domain, err)
	}

	admitDevice(t, manager, userID, state.Device)
}

// admitDevice records one of the reader's appliances on the peer.
//
// It is the half of C22 that only the database will tell you about: every
// operation names the device that authored it and sync.operations references
// identity.devices, so a peer that holds the reader and not their devices
// refuses the whole batch on a foreign key. The obligation is also a standing
// one rather than a handshake — a device bound tomorrow has to reach every
// replica before anything it writes can — which is the strongest argument that
// what is missing is a call and not a manual step.
func admitDevice(t *testing.T, manager *persist.Manager, userID uuid.UUID, appliance client.Device) {
	t.Helper()

	name, err := identitydevice.ParseName(appliance.Name)
	if err != nil {
		t.Fatalf("the device's name: %v", err)
	}

	platform, err := identitydevice.ParsePlatform(appliance.Platform)
	if err != nil {
		t.Fatalf("the device's platform: %v", err)
	}

	recorded := identitydevice.Restore(appliance.ID, &identitydevice.Props{
		UserID:   userID,
		Name:     name,
		Platform: platform,
		Active:   true,
	})

	if err = devicerepository.New(manager).Create(t.Context(), recorded); err != nil &&
		!errors.Is(err, errs.KindAlreadyExists) {
		t.Fatalf("recording the device: %v", err)
	}
}

// admitPeer records `from` in the catalogue of `to`, out of the document
// `from` publishes about itself, and returns the row's identifier.
//
// The federation is long-lived, so a node already recorded by an earlier run is
// read rather than written again — which is also what the real call would have
// to do.
func admitPeer(t *testing.T, to, from *node) uuid.UUID {
	t.Helper()

	catalogue := serverrepository.New(persist.NewManager(connect(t, to)))
	domain := federationserver.ParseDomain(from.domain)

	switch known, err := catalogue.GetByDomain(t.Context(), domain); {
	case err == nil:
		return known.ID
	case !errors.Is(err, errs.KindNotFound):
		t.Fatalf("reading the catalogue of %s: %v", to.domain, err)
	}

	published := describes(t, from)

	recorded, err := federationserver.New(&federationserver.Descriptor{
		Domain:                 domain,
		BaseURL:                federationserver.BaseURL(published.Server.BaseURL),
		JWKSURI:                federationserver.JWKSURI(published.Server.JWKSURI),
		CertificateFingerprint: federationserver.Fingerprint(published.Server.CertificateFingerprint),
		GRPCAuthority:          federationserver.GRPCAuthority(published.Server.GRPC),
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("describing %s: %v", from.domain, err)
	}

	if err = catalogue.Create(t.Context(), recorded); err != nil {
		t.Fatalf("recording %s on %s: %v", from.domain, to.domain, err)
	}

	return recorded.ID
}

// repin replaces the pin a node recorded for a peer, and puts the recorded one
// back when the test ends.
//
// It is the one thing here a reader cannot do through the contract, and
// deliberately so: RefreshKnownServer re-reads the peer's own document, so it
// can only ever record what the peer really presents. A test that wants a
// wrong pin has to write one.
//
// Putting it back is not tidiness. The catalogue is node-wide (C15), so a
// wrong pin left behind is a wrong pin for every reader of this node and for
// every later run of this suite.
func repin(t *testing.T, on *node, server uuid.UUID, pin string) {
	t.Helper()

	previous := setPin(t, on, server, federationserver.Fingerprint(pin))

	t.Cleanup(func() { setPin(t, on, server, previous) })
}

// setPin writes a pin into a node's catalogue and returns the one it replaced.
//
// It opens and closes its own pool rather than taking one from [connect],
// because it is called from a cleanup: what a test closed on its way out is
// not available to the code putting the federation back as it was.
func setPin(
	t *testing.T, on *node, server uuid.UUID, pin federationserver.Fingerprint,
) federationserver.Fingerprint {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), on.databaseURL)
	if err != nil {
		t.Fatalf("connecting to the database of %s: %v", on.domain, err)
	}

	defer pool.Close()

	catalogue := serverrepository.New(persist.NewManager(pool))

	recorded, err := catalogue.GetByID(context.Background(), server)
	if err != nil {
		t.Fatalf("reading the peer on %s: %v", on.domain, err)
	}

	props := recorded.Props
	previous := props.CertificateFingerprint
	props.CertificateFingerprint = pin

	if err = catalogue.Update(context.Background(), federationserver.Restore(recorded.ID, &props)); err != nil {
		t.Fatalf("changing the pin on %s: %v", on.domain, err)
	}

	return previous
}

// describes reads what a node publishes about itself, over the same plain HTTP
// the other node's discovery client reads it over.
func describes(t *testing.T, published *node) wellknown.ServerDocument {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		published.baseURL+wellknown.ServerPath, http.NoBody)
	if err != nil {
		t.Fatalf("addressing %s: %v", published.domain, err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("reading what %s publishes: %v", published.domain, err)
	}

	defer func() { _ = response.Body.Close() }()

	var document wellknown.ServerDocument
	if err = json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decoding what %s publishes: %v", published.domain, err)
	}

	return document
}

// connect opens a pool on a node's own database.
//
// It is the one thing in this suite that is not a client of the contract, and
// every use of it is either an assertion about what a node stored or the
// standing-in of C22. A pool per test is more than the federation needs and
// less than a cache nobody closes.
func connect(t *testing.T, on *node) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), on.databaseURL)
	if err != nil {
		t.Fatalf("connecting to the database of %s: %v", on.domain, err)
	}

	t.Cleanup(pool.Close)

	return pool
}
