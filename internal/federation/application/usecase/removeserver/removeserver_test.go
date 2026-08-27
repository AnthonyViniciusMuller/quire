package removeserver_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/removeserver"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// now is the instant the catalogue below was written at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase     *removeserver.RemoveServer
	servers     *apptest.ServerRepository
	replicas    *apptest.ReplicaRepository
	transaction *apptest.Transaction
	peer        *server.Server
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	servers := apptest.NewServerRepository()
	replicas := apptest.NewReplicaRepository()

	peer, err := server.New(apptest.Descriptor("quire-b.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if err := servers.Create(t.Context(), peer); err != nil {
		t.Fatalf("Create: %v", err)
	}

	transaction := apptest.NewTransaction()

	return fixture{
		usecase:     removeserver.New(servers, replicas, transaction),
		servers:     servers,
		replicas:    replicas,
		transaction: transaction,
		peer:        peer,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), removeserver.Input{ServerID: f.peer.ID}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if f.servers.Count() != 0 {
		t.Error("the node is still in the catalogue")
	}

	// The lock and the unit of work are what make the refusal below hold
	// against a reader authorizing the node at the same moment.
	if f.transaction.Calls() != 1 {
		t.Errorf("units of work = %d, want the one the check and the delete share", f.transaction.Calls())
	}

	if locked := f.servers.Locked(); len(locked) != 1 || locked[0] != f.peer.ID {
		t.Errorf("locked = %v, want the row the check is about", locked)
	}
}

// TestExecuteRefusesANodeSomebodyAuthorizes is RN03 from the other side.
// Forgetting a peer that still holds a reader's data would leave that reader
// unable to revoke it — and the foreign key cascades, so the row that proves
// they once authorized it would go too.
func TestExecuteRefusesANodeSomebodyAuthorizes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	granted, err := replica.New(uuid.New(), f.peer.ID, true, now)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	if created := f.replicas.Create(t.Context(), granted); created != nil {
		t.Fatalf("Create: %v", created)
	}

	_, err = f.usecase.Execute(t.Context(), removeserver.Input{ServerID: f.peer.ID})
	if err == nil {
		t.Fatal("Execute for an authorized node = nil, want an error")
	}

	if !errors.Is(err, errs.KindFailedPrecondition) || errs.CodeOf(err) != server.CodeServerInUse {
		t.Errorf("error = %v, want a failed precondition coded %q", err, server.CodeServerInUse)
	}

	if f.servers.Count() != 1 {
		t.Error("the node was forgotten anyway, and the authorization went with it")
	}
}

// TestExecuteForgetsANodeOnlyRevokedAuthorizationsName covers the other half
// of the count: a revoked row explains a peer that once held data, and it does
// not keep the node in the catalogue for ever.
func TestExecuteForgetsANodeOnlyRevokedAuthorizationsName(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	granted, err := replica.New(uuid.New(), f.peer.ID, true, now)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	granted.Revoke()

	if err := f.replicas.Create(t.Context(), granted); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := f.usecase.Execute(t.Context(), removeserver.Input{ServerID: f.peer.ID}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if f.servers.Count() != 0 {
		t.Error("a node only revoked authorizations named was kept")
	}
}

// TestExecuteRefusesThisInstance covers the row every reader hosted here
// references.
func TestExecuteRefusesThisInstance(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	local, err := f.servers.EnsureLocal(t.Context(), apptest.Descriptor("quire-a.example"))
	if err != nil {
		t.Fatalf("EnsureLocal: %v", err)
	}

	_, err = f.usecase.Execute(t.Context(), removeserver.Input{ServerID: local.ID})
	if err == nil {
		t.Fatal("Execute for this instance = nil, want an error")
	}

	if errs.CodeOf(err) != server.CodeLocalServer {
		t.Errorf("code = %q, want %q", errs.CodeOf(err), server.CodeLocalServer)
	}

	if f.servers.Count() != 2 {
		t.Error("this instance was forgotten, and every reader hosted here with it")
	}
}

func TestExecuteOfANodeNobodyKnows(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), removeserver.Input{ServerID: uuid.New()})
	if err == nil {
		t.Fatal("Execute for a node nobody knows = nil, want an error")
	}

	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("error = %v, want a not-found", err)
	}
}
