package listservers_test

import (
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listservers"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// now is the instant the catalogue below was written at.
var now = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// catalogue is three peers, the middle one deactivated, and this instance.
func catalogue(t *testing.T) *apptest.ServerRepository {
	t.Helper()

	servers := apptest.NewServerRepository()

	if _, err := servers.EnsureLocal(t.Context(), apptest.Descriptor("quire-a.example")); err != nil {
		t.Fatalf("EnsureLocal: %v", err)
	}

	for _, domain := range []server.Domain{"quire-c.example", "quire-b.example"} {
		node, err := server.New(apptest.Descriptor(domain), now)
		if err != nil {
			t.Fatalf("server.New: %v", err)
		}

		if domain == "quire-c.example" {
			if err := node.SetActive(false); err != nil {
				t.Fatalf("SetActive: %v", err)
			}
		}

		if err := servers.Create(t.Context(), node); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	return servers
}

// TestExecuteHidesTheDeactivatedAndKeepsThisInstance covers both defaults of
// the list. A node the reader stopped is not shown unless asked for, and this
// instance always is: a reader points at their origin server whether it is
// local or remote, and a list without it could not show them where they live.
func TestExecuteHidesTheDeactivatedAndKeepsThisInstance(t *testing.T) {
	t.Parallel()

	output, err := listservers.New(catalogue(t)).Execute(t.Context(), listservers.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Servers) != 2 {
		t.Fatalf("servers = %d, want the two that take part", len(output.Servers))
	}

	if output.Servers[0].Domain != "quire-a.example" || !output.Servers[0].IsLocal {
		t.Error("the list is not ordered by domain, or this instance is missing from it")
	}

	if output.Servers[1].Domain != "quire-b.example" {
		t.Errorf("the second entry is %q", output.Servers[1].Domain)
	}
}

func TestExecuteIncludingTheDeactivated(t *testing.T) {
	t.Parallel()

	output, err := listservers.New(catalogue(t)).
		Execute(t.Context(), listservers.Input{IncludeInactive: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Servers) != 3 {
		t.Fatalf("servers = %d, want every node the instance knows", len(output.Servers))
	}

	if output.Servers[2].Domain != "quire-c.example" || output.Servers[2].Active {
		t.Error("the deactivated node is missing, or came back taking part")
	}
}
