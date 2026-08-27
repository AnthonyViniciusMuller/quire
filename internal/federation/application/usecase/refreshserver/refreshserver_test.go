package refreshserver_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/refreshserver"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// now is the instant the node below was discovered at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase   *refreshserver.RefreshServer
	servers   *apptest.ServerRepository
	discovery *apptest.Discovery
	clock     *apptest.Clock
	peer      *server.Server
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	servers := apptest.NewServerRepository()
	discovery := apptest.NewDiscovery()
	clock := apptest.NewClock(now)

	discovery.Publish(apptest.Descriptor("quire-b.example"))

	peer, err := server.New(apptest.Descriptor("quire-b.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if err := servers.Create(t.Context(), peer); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return fixture{
		usecase:   refreshserver.New(servers, discovery, clock),
		servers:   servers,
		discovery: discovery,
		clock:     clock,
		peer:      peer,
	}
}

// TestExecuteOfARenewedCertificateReportsNothing is the case C12 exists to
// keep quiet. The pin is over the public key, so an ACME renewal keeps it —
// and a refresh that cried rotation every sixty days would be the alarm an
// operator learns to clear without looking.
func TestExecuteOfARenewedCertificateReportsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.clock.Advance(24 * time.Hour)

	output, err := f.usecase.Execute(t.Context(), refreshserver.Input{ServerID: f.peer.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.FingerprintChanged {
		t.Error("a node that kept its key was reported as having rotated it")
	}

	if !output.Server.DiscoveredAt.Equal(now.Add(24 * time.Hour)) {
		t.Error("the row does not say when the description was last learned")
	}
}

// TestExecuteReportsAMovedPin is the other half. The new value is stored —
// there is nothing here to check it against, and a record holding the old one
// could not be used against the node as it is now — and the reader is told,
// because a deliberate rotation and an interception look identical from this
// side.
func TestExecuteReportsAMovedPin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	rotated := apptest.Descriptor("quire-b.example")
	rotated.CertificateFingerprint = server.Fingerprint(wellknown.PinPrefix + "YmF6cXV1eAo=")
	f.discovery.Publish(rotated)

	output, err := f.usecase.Execute(t.Context(), refreshserver.Input{ServerID: f.peer.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !output.FingerprintChanged {
		t.Error("the reader was not told the node presents a different key, which is theirs to judge")
	}

	stored, err := f.servers.GetByID(t.Context(), f.peer.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.CertificateFingerprint != rotated.CertificateFingerprint {
		t.Error("the pin on record is still the old one, which nothing the node presents would match")
	}
}

// TestExecuteAddressesTheLookupToTheDomainOnRecord covers what makes this a
// refresh of a node rather than a way to point a row at another one.
func TestExecuteAddressesTheLookupToTheDomainOnRecord(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), refreshserver.Input{ServerID: f.peer.ID}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lookups := f.discovery.Lookups()
	if len(lookups) != 1 || lookups[0] != "quire-b.example" {
		t.Errorf("lookups = %v, want the domain on record", lookups)
	}
}

// TestExecuteLeavesTheRowAloneWhenTheLookupFails covers a peer that is down
// for an afternoon. What is on record is what the node last said about itself,
// and it is worth more than nothing: losing it would make an unreachable peer
// into one this instance has forgotten how to reach.
func TestExecuteLeavesTheRowAloneWhenTheLookupFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.discovery.Err = errs.New(errs.KindUnavailable, "that domain did not answer the lookup").
		WithCode(service.CodeDiscoveryUnreachable)

	if _, err := f.usecase.Execute(t.Context(), refreshserver.Input{ServerID: f.peer.ID}); err == nil {
		t.Fatal("Execute against a peer that is down = nil, want an error")
	}

	stored, err := f.servers.GetByID(t.Context(), f.peer.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.BaseURL == "" || !stored.Pinned() {
		t.Error("a failed lookup emptied the description the node had last given")
	}
}

func TestExecuteOfANodeNobodyKnows(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), refreshserver.Input{ServerID: uuid.New()}); err == nil {
		t.Fatal("Execute for a node nobody knows = nil, want an error")
	}

	if len(f.discovery.Lookups()) != 0 {
		t.Error("a lookup was made for a row that does not exist")
	}
}
