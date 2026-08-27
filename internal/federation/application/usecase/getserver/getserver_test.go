package getserver_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/getserver"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// now is the instant the catalogue below was written at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func TestExecute(t *testing.T) {
	t.Parallel()

	servers := apptest.NewServerRepository()

	node, err := server.New(apptest.Descriptor("quire-b.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if created := servers.Create(t.Context(), node); created != nil {
		t.Fatalf("Create: %v", created)
	}

	output, err := getserver.New(servers).Execute(t.Context(), getserver.Input{ServerID: node.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Server.Domain != "quire-b.example" {
		t.Errorf("Domain = %q", output.Server.Domain)
	}
}

// TestExecuteReadsADeactivatedNode covers what deactivation means: the node is
// still known, and what it is not is replicated to. A reader asking about one
// by name is entitled to the answer.
func TestExecuteReadsADeactivatedNode(t *testing.T) {
	t.Parallel()

	servers := apptest.NewServerRepository()

	node, err := server.New(apptest.Descriptor("quire-b.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if deactivated := node.SetActive(false); deactivated != nil {
		t.Fatalf("SetActive: %v", deactivated)
	}

	if created := servers.Create(t.Context(), node); created != nil {
		t.Fatalf("Create: %v", created)
	}

	output, err := getserver.New(servers).Execute(t.Context(), getserver.Input{ServerID: node.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Server.Active {
		t.Error("the row came back taking part in replication")
	}
}

func TestExecuteOfANodeNobodyKnows(t *testing.T) {
	t.Parallel()

	_, err := getserver.New(apptest.NewServerRepository()).
		Execute(t.Context(), getserver.Input{ServerID: uuid.New()})
	if err == nil {
		t.Fatal("Execute for a node nobody knows = nil, want an error")
	}

	if !errors.Is(err, errs.KindNotFound) || errs.CodeOf(err) != server.CodeNotFound {
		t.Errorf("error = %v, want a not-found coded %q", err, server.CodeNotFound)
	}
}
