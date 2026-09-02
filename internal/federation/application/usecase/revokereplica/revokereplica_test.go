package revokereplica_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/revokereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// now is the instant the decisions below were made at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// granted is a repository holding one reader's permission for one node.
func granted(t *testing.T, reader, node uuid.UUID) *apptest.ReplicaRepository {
	t.Helper()

	replicas := apptest.NewReplicaRepository()

	authorization, err := replica.New(reader, node, true, now)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	if created := replicas.Create(t.Context(), authorization); created != nil {
		t.Fatalf("Create: %v", created)
	}

	return replicas
}

// TestExecuteKeepsTheRow is RN03 as the reader experiences it. Revoking stops
// the replication; it does not reach into another operator's database, and the
// record that the permission once existed is what explains a peer that still
// holds data.
func TestExecuteKeepsTheRow(t *testing.T) {
	t.Parallel()

	reader, node := uuid.New(), uuid.New()
	replicas := granted(t, reader, node)

	if _, err := revokereplica.New(replicas, apptest.NewPeers()).Execute(t.Context(),
		revokereplica.Input{UserID: reader, ServerID: node}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	withdrawn, err := replicas.GetByPair(t.Context(), reader, node)
	if err != nil {
		t.Fatalf("the row was deleted rather than deactivated: %v", err)
	}

	switch {
	case withdrawn.Active:
		t.Error("the permission still stands")
	case !withdrawn.AuthorizedAt.Equal(now):
		t.Error("revocation overwrote when the reader had granted it")
	case !withdrawn.ReplicatesFiles:
		t.Error("revocation forgot what the permission had covered")
	}
}

// TestExecuteTwiceSucceeds covers the state the reader asked for. A call that
// failed the second time would have a client showing an error for a node it
// has already stopped.
func TestExecuteTwiceSucceeds(t *testing.T) {
	t.Parallel()

	reader, node := uuid.New(), uuid.New()
	usecase := revokereplica.New(granted(t, reader, node), apptest.NewPeers())

	for attempt := range 2 {
		if _, err := usecase.Execute(t.Context(),
			revokereplica.Input{UserID: reader, ServerID: node}); err != nil {
			t.Fatalf("revocation %d: %v", attempt+1, err)
		}
	}
}

// TestExecuteForAnotherReadersPermission covers the answer a stranger gets: it
// is the one a node that holds nothing of theirs gets, because the reply must
// not tell them which pairs somebody else has decided about.
func TestExecuteForAnotherReadersPermission(t *testing.T) {
	t.Parallel()

	node := uuid.New()
	replicas := granted(t, uuid.New(), node)

	_, err := revokereplica.New(replicas, apptest.NewPeers()).Execute(t.Context(),
		revokereplica.Input{UserID: uuid.New(), ServerID: node})
	if err == nil {
		t.Fatal("Execute for another reader's permission = nil, want an error")
	}

	if !errors.Is(err, errs.KindNotFound) || errs.CodeOf(err) != replica.CodeNotFound {
		t.Errorf("error = %v, want a not-found coded %q", err, replica.CodeNotFound)
	}
}

// The node is told, and a node that could not be told does not keep the
// reader from withdrawing what is theirs: the revocation here is what
// protects them, and it stands either way.
func TestExecuteTellsTheNodeAndStandsWhenItCannot(t *testing.T) {
	t.Parallel()

	reader, node := uuid.New(), uuid.New()
	peers := apptest.NewPeers()

	if _, err := revokereplica.New(granted(t, reader, node), peers).Execute(t.Context(),
		revokereplica.Input{UserID: reader, ServerID: node}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if told := peers.Withdrawn(node); len(told) != 1 || told[0] != reader {
		t.Errorf("the node was told %v withdrew, want the reader", told)
	}

	silent := apptest.NewPeers()
	silent.Err = errs.New(errs.KindUnavailable, "no route to host")
	replicas := granted(t, reader, node)

	if _, err := revokereplica.New(replicas, silent).Execute(t.Context(),
		revokereplica.Input{UserID: reader, ServerID: node}); err != nil {
		t.Fatalf("Execute against a node that cannot be told: %v", err)
	}

	withdrawn, err := replicas.GetByPair(t.Context(), reader, node)
	if err != nil || withdrawn.Active {
		t.Errorf("the permission still stands because another operator's node was down: %v", err)
	}
}
