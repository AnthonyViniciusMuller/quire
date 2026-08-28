package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/client"
)

// device writes a state file for a device that has logged in once, and returns
// its path.
func device(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "quirectl.json")

	encoded, err := json.Marshal(client.State{
		Server: client.Server{Address: "quire-a.example:9090", Domain: "quire-a.example"},
		User:   client.User{FederatedID: "@anthony:quire-a.example"},
		Device: client.Device{ID: uuid.New(), Name: "the tablet", Platform: "cli"},
	})
	if err != nil {
		t.Fatalf("encoding the state: %v", err)
	}

	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("writing the state: %v", err)
	}

	return path
}

// execute runs the program as a shell would, and returns what it printed.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	out := &bytes.Buffer{}
	err := run(t.Context(), args, out, out)

	return out.String(), err
}

// Somebody who typed the name of the program is asking what it does, which is
// not an error.
func TestTheProgramWithNoCommandPrintsWhatItDoes(t *testing.T) {
	printed, err := execute(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(printed, "Available Commands:") {
		t.Errorf("the program printed %q", printed)
	}
}

// The one command that answers without calling the node, which is what makes it
// the one a reader runs when the node is what they cannot reach.
func TestStatusReadsTheDeviceWithoutANode(t *testing.T) {
	printed, err := execute(t, "--state", device(t), "--offline", "sync", "status")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, expected := range []string{"@anthony:quire-a.example", "the tablet", "pending   0"} {
		if !strings.Contains(printed, expected) {
			t.Errorf("the status does not mention %q:\n%s", expected, printed)
		}
	}
}

// The state file may be named by the environment, which is what lets a
// demonstration or an end-to-end run export it once and then type commands.
func TestTheEnvironmentNamesTheStateFile(t *testing.T) {
	t.Setenv(stateVariable, device(t))

	printed, err := execute(t, "--offline", "sync", "status")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(printed, "the tablet") {
		t.Errorf("the status was taken from somewhere else:\n%s", printed)
	}
}

// A change authored offline is queued rather than refused, and the reader is
// told which of the two happened — the one thing about a change they cannot
// see for themselves.
func TestAWriteMadeOfflineSaysItWasQueued(t *testing.T) {
	state := device(t)

	printed, err := execute(t, "--state", state, "--offline",
		"collection", "create", "--name", "Modernismo")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(printed, "queued") {
		t.Errorf("the write printed %q", printed)
	}

	pending, err := execute(t, "--state", state, "--offline", "sync", "status")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(pending, "pending   1") {
		t.Errorf("the change was not kept:\n%s", pending)
	}
}

// A call only the node can answer says so, rather than failing at a dial that
// was never going to be made.
func TestAReadMadeOfflineSaysWhy(t *testing.T) {
	_, err := execute(t, "--state", device(t), "--offline", "whoami")
	if err == nil {
		t.Fatal("the client answered a call it cannot answer")
	}

	if described := describe(err); !strings.Contains(described, "offline") {
		t.Errorf("the failure reads %q", described)
	}
}
