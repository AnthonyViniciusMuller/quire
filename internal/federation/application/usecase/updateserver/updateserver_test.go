package updateserver_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/updateserver"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// now is the instant the catalogue below was written at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase     *updateserver.UpdateServer
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
		usecase:     updateserver.New(servers, replicas, transaction),
		servers:     servers,
		replicas:    replicas,
		transaction: transaction,
		peer:        peer,
	}
}

// authorize is one reader allowing the peer to hold a copy of their data.
func (f fixture) authorize(t *testing.T) {
	t.Helper()

	granted, err := replica.New(uuid.New(), f.peer.ID, false, now)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	if err := f.replicas.Create(t.Context(), granted); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(),
		updateserver.Input{ServerID: f.peer.ID, Active: false})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Server.Active {
		t.Error("the node still takes part in replication")
	}

	stored, err := f.servers.GetByID(t.Context(), f.peer.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if stored.Active {
		t.Error("the flag was answered with and not written")
	}

	if locked := f.servers.Locked(); len(locked) != 1 || locked[0] != f.peer.ID {
		t.Errorf("locked = %v, want the row the check is about", locked)
	}
}

// TestExecuteRefusesToStopANodeSomebodyAuthorizes is C15 as a rule a reader
// can hit. federation.servers.active is node-wide, so clearing it stops the
// replication of every reader who authorized that node — a reader who wants it
// stopped for themselves revokes their own authorization instead.
func TestExecuteRefusesToStopANodeSomebodyAuthorizes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.authorize(t)

	_, err := f.usecase.Execute(t.Context(),
		updateserver.Input{ServerID: f.peer.ID, Active: false})
	if err == nil {
		t.Fatal("Execute deactivating an authorized node = nil, want an error")
	}

	if !errors.Is(err, errs.KindFailedPrecondition) || errs.CodeOf(err) != server.CodeServerInUse {
		t.Errorf("error = %v, want a failed precondition coded %q", err, server.CodeServerInUse)
	}

	stored, err := f.servers.GetByID(t.Context(), f.peer.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if !stored.Active {
		t.Error("the refusal stopped the node anyway")
	}
}

// TestExecuteRestoresANodeSomebodyAuthorizes covers the other direction, which
// is not guarded: restoring a node somebody replicates to is what they wanted,
// and restoring one nobody does costs nothing.
func TestExecuteRestoresANodeSomebodyAuthorizes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(),
		updateserver.Input{ServerID: f.peer.ID, Active: false}); err != nil {
		t.Fatalf("stopping it: %v", err)
	}

	f.authorize(t)

	output, err := f.usecase.Execute(t.Context(),
		updateserver.Input{ServerID: f.peer.ID, Active: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !output.Server.Active {
		t.Error("the node was not restored")
	}
}

// TestExecuteRefusesThisInstance covers the row every reader hosted here
// references. A node that stopped replicating on its own behalf would have
// taken its own readers out of the federation.
func TestExecuteRefusesThisInstance(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	local, err := f.servers.EnsureLocal(t.Context(), apptest.Descriptor("quire-a.example"))
	if err != nil {
		t.Fatalf("EnsureLocal: %v", err)
	}

	_, err = f.usecase.Execute(t.Context(), updateserver.Input{ServerID: local.ID, Active: false})
	if err == nil {
		t.Fatal("Execute deactivating this instance = nil, want an error")
	}

	if errs.CodeOf(err) != server.CodeLocalServer {
		t.Errorf("code = %q, want %q", errs.CodeOf(err), server.CodeLocalServer)
	}
}
