package replica_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// granted is the instant the authorizations below were decided at.
var granted = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func TestNew(t *testing.T) {
	t.Parallel()

	reader, node := uuid.New(), uuid.New()

	authorization, err := replica.New(reader, node, true, granted)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case authorization.ID == (uuid.UUID{}):
		t.Error("the authorization was granted without an identifier")
	case !authorization.BelongsTo(reader):
		t.Error("the authorization does not name the reader whose data it covers")
	case authorization.ServerID != node:
		t.Error("the authorization does not name the node that may hold the copy")
	case !authorization.ReplicatesFiles:
		t.Error("the files were left out of a permission that covered them")
	case !authorization.Active:
		t.Error("a permission just granted does not stand")
	}
}

func TestNewRejectsAnIncompleteDecision(t *testing.T) {
	t.Parallel()

	reader, node := uuid.New(), uuid.New()

	cases := map[string]struct {
		userID, serverID uuid.UUID
		at               time.Time
		field            string
	}{
		"no reader": {uuid.UUID{}, node, granted, "user_id"},
		"no node":   {reader, uuid.UUID{}, granted, "server_id"},
		"no time":   {reader, node, time.Time{}, "authorized_at"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := replica.New(testCase.userID, testCase.serverID, false, testCase.at)
			if err == nil {
				t.Fatal("New = nil, want an error")
			}

			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("error = %v, want an invalid argument", err)
			}

			if got := errs.CodeOf(err); got != replica.CodeInvalidAuthorization {
				t.Errorf("code = %q, want %q", got, replica.CodeInvalidAuthorization)
			}

			fields := errs.FieldsOf(err)
			if len(fields) == 0 || fields[0].Name != testCase.field {
				t.Errorf("fields = %v, want the first to be %q", fields, testCase.field)
			}
		})
	}
}

// TestRevokeKeepsTheRow is RN03 as the reader experiences it. Revoking stops
// the replication; it does not reach into another operator's database, and the
// record that the permission once existed is what explains a peer that still
// holds data.
func TestRevokeKeepsTheRow(t *testing.T) {
	t.Parallel()

	authorization, err := replica.New(uuid.New(), uuid.New(), true, granted)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	identifier := authorization.ID

	authorization.Revoke()

	switch {
	case authorization.Active:
		t.Error("the permission still stands after being revoked")
	case authorization.ID != identifier:
		t.Error("revocation replaced the row, so the history of the decision is gone")
	case !authorization.AuthorizedAt.Equal(granted):
		t.Error("revocation overwrote when the reader had granted it")
	}
}

// TestGrantReusesTheRow covers the unique constraint on the pair: a reader who
// re-authorizes a node they had revoked writes that row, so a grant and its
// revocation stay in one place rather than becoming two histories of one
// decision.
func TestGrantReusesTheRow(t *testing.T) {
	t.Parallel()

	authorization, err := replica.New(uuid.New(), uuid.New(), false, granted)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	authorization.Revoke()

	later := granted.Add(72 * time.Hour)
	authorization.Grant(true, later)

	switch {
	case !authorization.Active:
		t.Error("the permission was not restored")
	case !authorization.ReplicatesFiles:
		t.Error("the second decision did not widen the permission to the files")
	case !authorization.AuthorizedAt.Equal(later):
		t.Error("the row still reports the first decision, not the one that stands")
	}
}

// TestBelongsToRefusesAnotherReader covers the check every call that names an
// authorization makes: one that is somebody else's is answered exactly as one
// that does not exist.
func TestBelongsToRefusesAnotherReader(t *testing.T) {
	t.Parallel()

	authorization, err := replica.New(uuid.New(), uuid.New(), false, granted)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if authorization.BelongsTo(uuid.New()) {
		t.Error("an authorization reported that it belongs to a reader who never granted it")
	}
}
