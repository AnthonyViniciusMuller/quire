package main

import (
	"strings"
	"testing"
)

// A node that cannot read its configuration has to say so and stop, not start
// half-configured and fail later where the reason is no longer visible.
func TestRunReportsAMisconfiguredNode(t *testing.T) {
	// One required variable emptied is enough to fail the load, whatever else
	// the environment running the test happens to carry.
	t.Setenv("QUIRE_SERVER_NAME", "")

	err := run(t.Context())
	if err == nil {
		t.Fatal("run started a node with no server name")
	}

	if !strings.Contains(err.Error(), "QUIRE_SERVER_NAME") {
		t.Errorf("run failed with %v, which does not name the variable at fault", err)
	}
}
