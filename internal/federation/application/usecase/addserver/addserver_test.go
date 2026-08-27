package addserver_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/addserver"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// now is the instant the catalogue is written at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// fixture is the use case with the doubles it was built over, so that a test
// can assert on what it wrote as well as on what it answered.
type fixture struct {
	usecase   *addserver.AddServer
	servers   *apptest.ServerRepository
	discovery *apptest.Discovery
}

func newFixture() fixture {
	servers := apptest.NewServerRepository()
	discovery := apptest.NewDiscovery()

	return fixture{
		usecase:   addserver.New(servers, discovery, apptest.NewClock(now)),
		servers:   servers,
		discovery: discovery,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.discovery.Publish(apptest.Descriptor("quire-b.example"))

	output, err := f.usecase.Execute(t.Context(), addserver.Input{Domain: "Quire-B.Example"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Server.Domain != "quire-b.example":
		t.Errorf("Domain = %q, want the folded form", output.Server.Domain)
	case output.Server.IsLocal:
		t.Error("a discovered peer was recorded as this instance")
	case !output.Server.Active:
		t.Error("a node the reader just added does not take part in replication")
	case !output.Server.Pinned():
		t.Error("the pin RNF08 is checked against was not recorded, so nothing anchors the peer")
	case !output.Server.DiscoveredAt.Equal(now):
		t.Error("the row does not say when its description was learned")
	}

	stored, err := f.servers.GetByDomain(t.Context(), "quire-b.example")
	if err != nil {
		t.Fatalf("the node was answered for and not written: %v", err)
	}

	if stored.ID != output.Server.ID {
		t.Error("the row that was written is not the one that was answered with")
	}
}

// TestExecuteDoesNotDiscoverANodeAlreadyKnown covers the one thing the
// pre-check buys, which the registration of a reader deliberately does not
// have: a request to a third party that did not have to be made. The
// catalogue is node-wide, so a domain another reader added first is the
// ordinary case rather than a race.
func TestExecuteDoesNotDiscoverANodeAlreadyKnown(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.discovery.Publish(apptest.Descriptor("quire-b.example"))

	if _, err := f.usecase.Execute(t.Context(), addserver.Input{Domain: "quire-b.example"}); err != nil {
		t.Fatalf("the first addition: %v", err)
	}

	_, err := f.usecase.Execute(t.Context(), addserver.Input{Domain: "quire-b.example"})
	if err == nil {
		t.Fatal("the second addition = nil, want an error")
	}

	if !errors.Is(err, errs.KindAlreadyExists) || errs.CodeOf(err) != server.CodeDomainKnown {
		t.Errorf("error = %v, want an already-exists coded %q", err, server.CodeDomainKnown)
	}

	if lookups := f.discovery.Lookups(); len(lookups) != 1 {
		t.Errorf("lookups = %v, want only the one the first addition needed", lookups)
	}

	if f.servers.Count() != 1 {
		t.Error("the catalogue holds a second row for one node, and therefore two pins for one key")
	}
}

// TestExecuteWritesNothingWhenTheLookupFails covers the order the two steps
// happen in: a node that could not be discovered has no description, and a row
// without one would name a peer nothing could reach or check.
func TestExecuteWritesNothingWhenTheLookupFails(t *testing.T) {
	t.Parallel()

	f := newFixture()

	_, err := f.usecase.Execute(t.Context(), addserver.Input{Domain: "quire-b.example"})
	if err == nil {
		t.Fatal("Execute against a host that publishes nothing = nil, want an error")
	}

	if got := errs.CodeOf(err); got != service.CodeNotAQuireServer {
		t.Errorf("code = %q, want %q", got, service.CodeNotAQuireServer)
	}

	if f.servers.Count() != 0 {
		t.Error("a node that could not be discovered was recorded anyway")
	}
}

// TestExecuteRefusesSomethingThatIsNotAHost covers the input the reader typed,
// which never reaches the network or the catalogue.
func TestExecuteRefusesSomethingThatIsNotAHost(t *testing.T) {
	t.Parallel()

	f := newFixture()

	_, err := f.usecase.Execute(t.Context(), addserver.Input{Domain: "https://quire-b.example"})
	if err == nil {
		t.Fatal("Execute with something that is not a host = nil, want an error")
	}

	if errs.CodeOf(err) != server.CodeInvalidDomain {
		t.Errorf("code = %q, want %q", errs.CodeOf(err), server.CodeInvalidDomain)
	}

	if len(f.discovery.Lookups()) != 0 || f.servers.Count() != 0 {
		t.Error("the lookup was made, or the row written, out of a value that is not a host")
	}
}
