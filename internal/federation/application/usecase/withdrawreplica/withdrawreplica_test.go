package withdrawreplica_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/withdrawreplica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

var now = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase  *withdrawreplica.WithdrawReplica
	replicas *apptest.ReplicaRepository
	origin   *server.Server
	reader   uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	servers := apptest.NewServerRepository()
	replicas := apptest.NewReplicaRepository()

	origin, err := server.New(apptest.Descriptor("quire-a.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if err = servers.Create(t.Context(), origin); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase:  withdrawreplica.New(servers, replicas),
		replicas: replicas,
		origin:   origin,
		reader:   uuid.New(),
	}
}

// admitted records the permission the origin once carried here.
func (f *fixture) admitted(t *testing.T) *replica.Replica {
	t.Helper()

	granted, err := replica.New(f.reader, f.origin.ID, true, now)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	if err = f.replicas.Create(t.Context(), granted); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return granted
}

func TestExecuteDeactivatesAndKeepsTheRow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	granted := f.admitted(t)

	_, err := f.usecase.Execute(t.Context(), withdrawreplica.Input{
		Pin:    f.origin.CertificateFingerprint.String(),
		UserID: f.reader,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	held, err := f.replicas.GetByPair(t.Context(), f.reader, f.origin.ID)
	if err != nil {
		t.Fatalf("the row was removed, and nothing explains why this node holds the reader's data: %v", err)
	}

	if held.Active || held.ID != granted.ID {
		t.Errorf("the permission is %+v, want the same row deactivated", held.Props)
	}
}

// A permission this node never recorded is nothing to withdraw, and a second
// withdrawal is not a failure: the origin retries what did not answer.
func TestExecuteSucceedsWithNothingToWithdraw(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), withdrawreplica.Input{
		Pin:    f.origin.CertificateFingerprint.String(),
		UserID: f.reader,
	})
	if err != nil {
		t.Errorf("Execute = %v, want nothing to withdraw to be no failure", err)
	}
}

func TestExecuteRefusesACallerTheCatalogueDoesNotName(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.admitted(t)

	_, err := f.usecase.Execute(t.Context(), withdrawreplica.Input{Pin: "sha256:AAAA", UserID: f.reader})
	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("Execute = %v, want the caller refused", err)
	}

	held, err := f.replicas.GetByPair(t.Context(), f.reader, f.origin.ID)
	if err != nil || !held.Active {
		t.Errorf("a caller the catalogue does not name withdrew a permission: %v", err)
	}
}
