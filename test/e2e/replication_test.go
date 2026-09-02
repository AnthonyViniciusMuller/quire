//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
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
// (C12); before it offers anything of the reader's it tells the peer who they
// are, through the call C22 adds, and the peer records them only if the origin
// is in its own catalogue; the delivery queue is filled from the log rather
// than by the call that wrote the change, which is what makes a peer
// authorized today and a peer that missed a week the same case; and the
// connection is mTLS, checked against that pin on both ends rather than
// against an authority neither of them shares.
//
// Nothing here writes into either node's database to make the federation
// work. The one write that remains, [repin], breaks it on purpose.
func TestAnAuthorizedNodeIsSentTheReadersChanges(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	peer := addKnownServer(t, tablet, nodeB.domain)

	if _, err := tablet.AuthorizeReplica(t.Context(), peer, true); err != nil {
		t.Fatalf("authorizing %s to replicate: %v", nodeB.domain, err)
	}

	federate(t, nodeB, nodeA)

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

// RN03, withdrawn: a node the reader stops allowing is not sent what they
// write afterwards. The origin stops offering, and it tells the node — the
// mirror of C22 — so that the node stops accepting as well.
func TestARevokedNodeIsNotSentTheReadersChanges(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	peer := addKnownServer(t, tablet, nodeB.domain)
	federate(t, nodeB, nodeA)

	if _, err := tablet.AuthorizeReplica(t.Context(), peer, false); err != nil {
		t.Fatalf("authorizing %s to replicate: %v", nodeB.domain, err)
	}

	works := ebookrepository.New(persist.NewManager(connect(t, nodeB)))

	tablet.disconnect(t)
	first := createWork(t, tablet, "Sagarana")
	tablet.reconnect(t)
	push(t, tablet)

	eventually(t, "the first work to reach "+nodeB.domain, func() bool {
		_, err := works.GetByID(t.Context(), first)

		return err == nil
	})

	if err := tablet.RevokeReplica(t.Context(), peer); err != nil {
		t.Fatalf("revoking %s: %v", nodeB.domain, err)
	}

	tablet.disconnect(t)
	second := createWork(t, tablet, "Corpo de Baile")
	tablet.reconnect(t)
	push(t, tablet)

	time.Sleep(settleFor / 3)

	if _, err := works.GetByID(t.Context(), second); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("%s holds a work written after the reader withdrew its permission: %v", nodeB.domain, err)
	}
}

func TestAPeerIsRefusedWhenItsKeyIsNotTheOnePinned(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	peer := addKnownServer(t, tablet, nodeB.domain)

	if _, err := tablet.AuthorizeReplica(t.Context(), peer, false); err != nil {
		t.Fatalf("authorizing %s to replicate: %v", nodeB.domain, err)
	}

	federate(t, nodeB, nodeA)
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

// federate makes `to` know `from`, the way a node comes to know another
// through the contract: somebody with a session on `to` adds it.
//
// It is the half of UC15 the specification leaves to the destination's own
// readers, and C22 is why it is required: a node records a reader only for
// an origin its own catalogue names, so that a node anybody could tell about
// readers is not a node anybody could fill with them. The federation is
// long-lived, so a node already known from an earlier run is read rather
// than added again — which is also what a reader would find.
func federate(t *testing.T, to, from *node) {
	t.Helper()

	host := newReader(t, to)
	appliance := newDevice(t, to, host, "the operator's laptop")

	addKnownServer(t, appliance, from.domain)
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

func connect(t *testing.T, on *node) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), on.databaseURL)
	if err != nil {
		t.Fatalf("connecting to the database of %s: %v", on.domain, err)
	}

	t.Cleanup(pool.Close)

	return pool
}
