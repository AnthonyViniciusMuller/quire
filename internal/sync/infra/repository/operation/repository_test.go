package operation

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/persist/syncdb"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, reader, phone, work := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

	op, err := toDomain(&syncdb.SyncOperation{
		ID:           id,
		UserID:       reader,
		DeviceID:     phone,
		Position:     17,
		TargetEntity: "ebook",
		TargetID:     work,
		Operation:    "update",
		Delta:        json.RawMessage(`{"title":"Vidas Secas"}`),
		VectorClock:  crdt.VectorClock{crdt.Author(phone): 2},
		CreatedAt:    at,
	}, "test")
	if err != nil {
		t.Fatalf("toDomain: %v", err)
	}

	switch {
	case op.ID != id:
		t.Error("the row was rebuilt under a new identifier, so no node could recognize it as one it had")
	case op.UserID != reader || !op.IsAuthoredBy(phone):
		t.Error("the operation lost the reader or the device it names")
	case op.Position != 17:
		t.Errorf("the operation came back at position %d", op.Position)
	case op.Target.Entity != operation.TargetEbook || op.Target.ID != work:
		t.Errorf("the target came back as %s", op.Target)
	case op.Kind != operation.KindUpdate:
		t.Errorf("the kind of change came back as %q", op.Kind)
	case !op.Delta.Claims("title"):
		t.Errorf("the delta came back claiming %v", op.Delta.Fields())
	case op.VectorClock.Get(crdt.Author(phone)) != 2:
		t.Errorf("the causal version came back as %s", op.VectorClock)
	case !op.CreatedAt.Equal(at):
		t.Errorf("the instant came back as %s", op.CreatedAt)
	}
}

// An operation naming an entity a later version added is replicated back here,
// and a row this node can no longer parse must be merely unfamiliar rather
// than unreadable — which is what casting the value objects instead of parsing
// them buys, and what makes adding a replicable entity a change that does not
// break the federation.
func TestToDomainKeepsAnEntityThisNodeDoesNotKnow(t *testing.T) {
	t.Parallel()

	op, err := toDomain(&syncdb.SyncOperation{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		DeviceID:     uuid.New(),
		TargetEntity: "shelf",
		TargetID:     uuid.New(),
		Operation:    "reorder",
		Delta:        json.RawMessage(`{}`),
	}, "test")
	if err != nil {
		t.Fatalf("toDomain refused a row a later version wrote: %v", err)
	}

	if op.Target.Entity != "shelf" || op.Kind != "reorder" {
		t.Errorf("the row was rewritten on the way out: %s, %q", op.Target, op.Kind)
	}
}

// A deletion claims no field, and the column holds that as {} rather than as
// null — the constraint refuses anything that is not an object.
func TestToDomainOfADeletionThatClaimsNothing(t *testing.T) {
	t.Parallel()

	op, err := toDomain(&syncdb.SyncOperation{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		DeviceID:     uuid.New(),
		TargetEntity: "annotation",
		TargetID:     uuid.New(),
		Operation:    "delete",
		Delta:        json.RawMessage(`{}`),
	}, "test")
	if err != nil {
		t.Fatalf("toDomain: %v", err)
	}

	if !op.Delta.IsEmpty() {
		t.Errorf("the deletion came back claiming %v", op.Delta.Fields())
	}

	if !op.Revision().Deleted {
		t.Error("the deletion did not produce a tombstone")
	}
}
