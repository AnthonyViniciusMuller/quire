package authorizereplica_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/authorizereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// now is the instant the decisions below are made at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase     *authorizereplica.AuthorizeReplica
	servers     *apptest.ServerRepository
	replicas    *apptest.ReplicaRepository
	clock       *apptest.Clock
	transaction *apptest.Transaction
	reader      uuid.UUID
	peer        *server.Server
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	servers := apptest.NewServerRepository()
	replicas := apptest.NewReplicaRepository()
	clock := apptest.NewClock(now)
	transaction := apptest.NewTransaction()

	peer, err := server.New(apptest.Descriptor("quire-b.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if created := servers.Create(t.Context(), peer); created != nil {
		t.Fatalf("Create: %v", created)
	}

	return fixture{
		usecase:     authorizereplica.New(servers, replicas, clock, transaction),
		servers:     servers,
		replicas:    replicas,
		clock:       clock,
		transaction: transaction,
		reader:      uuid.New(),
		peer:        peer,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), authorizereplica.Input{
		UserID:          f.reader,
		ServerID:        f.peer.ID,
		ReplicatesFiles: true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case !output.Authorization.BelongsTo(f.reader):
		t.Error("the permission does not name the reader whose data it covers")
	case output.Authorization.ServerID != f.peer.ID:
		t.Error("the permission does not name the node that may hold the copy")
	case !output.Authorization.ReplicatesFiles:
		t.Error("the files were left out of a permission that covered them")
	case !output.Authorization.Active:
		t.Error("a permission just granted does not stand")
	case !output.Authorization.AuthorizedAt.Equal(now):
		t.Error("the row does not say when the reader decided")
	}

	// The lock is what makes forgetting or stopping the node refuse against a
	// grant that arrives at the same moment.
	if locked := f.servers.Locked(); len(locked) != 1 || locked[0] != f.peer.ID {
		t.Errorf("locked = %v, want the node the grant is about", locked)
	}

	if f.transaction.Calls() != 1 {
		t.Errorf("units of work = %d, want the one the lock is held for", f.transaction.Calls())
	}
}

// TestExecuteReusesTheRow covers the unique constraint on the pair. A reader
// who widens their permission, or restores one they had withdrawn, writes the
// row they already have — so a grant and its revocation stay in one place
// rather than becoming two histories of one decision.
func TestExecuteReusesTheRow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	first, err := f.usecase.Execute(t.Context(), authorizereplica.Input{
		UserID:   f.reader,
		ServerID: f.peer.ID,
	})
	if err != nil {
		t.Fatalf("the first grant: %v", err)
	}

	f.clock.Advance(72 * time.Hour)

	second, err := f.usecase.Execute(t.Context(), authorizereplica.Input{
		UserID:          f.reader,
		ServerID:        f.peer.ID,
		ReplicatesFiles: true,
	})
	if err != nil {
		t.Fatalf("the second grant: %v", err)
	}

	switch {
	case second.Authorization.ID != first.Authorization.ID:
		t.Error("the second decision wrote a second row, and the pair now has two histories")
	case !second.Authorization.ReplicatesFiles:
		t.Error("the permission was not widened to the files")
	case !second.Authorization.AuthorizedAt.Equal(now.Add(72 * time.Hour)):
		t.Error("the row still reports the first decision rather than the one that stands")
	}
}

// TestExecuteRefusesThisInstance covers the row that would say nothing. A
// reader hosted here does not authorize a replica of themselves on the node
// they already live on — and the row would then refuse to let this instance be
// deactivated.
func TestExecuteRefusesThisInstance(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	local, err := f.servers.EnsureLocal(t.Context(), apptest.Descriptor("quire-a.example"))
	if err != nil {
		t.Fatalf("EnsureLocal: %v", err)
	}

	_, err = f.usecase.Execute(t.Context(),
		authorizereplica.Input{UserID: f.reader, ServerID: local.ID})
	if err == nil {
		t.Fatal("Execute for this instance = nil, want an error")
	}

	if !errors.Is(err, errs.KindFailedPrecondition) || errs.CodeOf(err) != server.CodeLocalServer {
		t.Errorf("error = %v, want a failed precondition coded %q", err, server.CodeLocalServer)
	}
}

// TestExecuteRefusesADeactivatedNode covers a promise nothing would keep: the
// replication worker walks the nodes that take part, so an authorization for
// one that does not would say the data may travel somewhere nothing sends it —
// and it would immediately refuse to let that node be forgotten.
func TestExecuteRefusesADeactivatedNode(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if deactivated := f.peer.SetActive(false); deactivated != nil {
		t.Fatalf("SetActive: %v", deactivated)
	}

	if err := f.servers.Update(t.Context(), f.peer); err != nil {
		t.Fatalf("Update: %v", err)
	}

	_, err := f.usecase.Execute(t.Context(),
		authorizereplica.Input{UserID: f.reader, ServerID: f.peer.ID})
	if err == nil {
		t.Fatal("Execute for a deactivated node = nil, want an error")
	}

	if errs.CodeOf(err) != server.CodeServerInactive {
		t.Errorf("code = %q, want %q", errs.CodeOf(err), server.CodeServerInactive)
	}
}

// TestExecuteRefusesANodeNobodyKnows covers what makes the pin the reader is
// trusting one this instance actually learned: the node has to be in the
// catalogue, so there is no way to authorize a domain nobody discovered.
func TestExecuteRefusesANodeNobodyKnows(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(),
		authorizereplica.Input{UserID: f.reader, ServerID: uuid.New()})
	if err == nil {
		t.Fatal("Execute for a node nobody knows = nil, want an error")
	}

	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("error = %v, want a not-found", err)
	}
}
