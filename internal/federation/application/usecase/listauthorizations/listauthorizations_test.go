package listauthorizations_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listauthorizations"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// now is the instant the decisions below were made at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase  *listauthorizations.ListAuthorizations
	servers  *apptest.ServerRepository
	replicas *apptest.ReplicaRepository
	reader   uuid.UUID
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	servers := apptest.NewServerRepository()
	replicas := apptest.NewReplicaRepository()

	return fixture{
		usecase:  listauthorizations.New(servers, replicas),
		servers:  servers,
		replicas: replicas,
		reader:   uuid.New(),
	}
}

// authorize records the reader's decision about a node, which the fixture adds
// to the catalogue first.
func (f fixture) authorize(t *testing.T, domain server.Domain, at time.Time, active bool) uuid.UUID {
	t.Helper()

	node, err := server.New(apptest.Descriptor(domain), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if created := f.servers.Create(t.Context(), node); created != nil {
		t.Fatalf("Create: %v", created)
	}

	authorization, err := replica.New(f.reader, node.ID, false, at)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	if !active {
		authorization.Revoke()
	}

	if created := f.replicas.Create(t.Context(), authorization); created != nil {
		t.Fatalf("Create: %v", created)
	}

	return node.ID
}

// TestExecuteNamesTheNodes is why the reply carries a domain beside each
// identifier: a reader auditing RN03 has to be able to read the answer, and a
// list of uuids is not one.
func TestExecuteNamesTheNodes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.authorize(t, "quire-b.example", now, true)

	output, err := f.usecase.Execute(t.Context(), listauthorizations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Authorizations) != 1 {
		t.Fatalf("authorizations = %d, want the one the reader granted", len(output.Authorizations))
	}

	if output.Authorizations[0].ServerDomain != "quire-b.example" {
		t.Errorf("ServerDomain = %q, and a client would have to ask again to name the node",
			output.Authorizations[0].ServerDomain)
	}
}

// TestExecuteHidesTheWithdrawnUnlessAsked covers the rows that explain a peer
// which still holds data. They are the ones a reader most needs on request and
// least needs by default.
func TestExecuteHidesTheWithdrawnUnlessAsked(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.authorize(t, "quire-b.example", now, true)
	f.authorize(t, "quire-c.example", now.Add(time.Hour), false)

	standing, err := f.usecase.Execute(t.Context(), listauthorizations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(standing.Authorizations) != 1 || standing.Authorizations[0].ServerDomain != "quire-b.example" {
		t.Fatalf("authorizations = %+v, want only the one that stands", standing.Authorizations)
	}

	everything, err := f.usecase.Execute(t.Context(),
		listauthorizations.Input{UserID: f.reader, IncludeInactive: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(everything.Authorizations) != 2 {
		t.Fatalf("authorizations = %d, want both decisions", len(everything.Authorizations))
	}

	// Newest decision first, which is the order the statement returns.
	if everything.Authorizations[0].ServerDomain != "quire-c.example" {
		t.Errorf("the list is not ordered by when the reader decided: %+v", everything.Authorizations)
	}
}

// TestExecuteNamesADeactivatedNode covers why the catalogue is read whole. A
// permission for a node that was later stopped still has to be shown with a
// name, and it is one of the rows a reader most needs to see.
func TestExecuteNamesADeactivatedNode(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.authorize(t, "quire-b.example", now, true)

	node, err := f.servers.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if deactivated := node.SetActive(false); deactivated != nil {
		t.Fatalf("SetActive: %v", deactivated)
	}

	if updated := f.servers.Update(t.Context(), node); updated != nil {
		t.Fatalf("Update: %v", updated)
	}

	output, err := f.usecase.Execute(t.Context(), listauthorizations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Authorizations) != 1 || output.Authorizations[0].ServerDomain != "quire-b.example" {
		t.Errorf("authorizations = %+v, want the deactivated node named", output.Authorizations)
	}
}

// TestExecuteForAReaderWhoAuthorizedNobody answers with an empty list and not
// with nil, so that a client ranging over it and one comparing it to nothing
// behave the same.
func TestExecuteForAReaderWhoAuthorizedNobody(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), listauthorizations.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Authorizations == nil || len(output.Authorizations) != 0 {
		t.Errorf("authorizations = %v, want an empty list", output.Authorizations)
	}
}
