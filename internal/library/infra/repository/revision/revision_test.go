package revision_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/infra/repository/revision"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestToColumnsCompactsTheClock(t *testing.T) {
	t.Parallel()

	phone, tablet := uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	columns := revision.ToColumns(crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 2, crdt.Author(tablet): 0},
		UpdatedAt:   at,
		DeviceID:    phone,
	})

	if columns.VectorClock.Len() != 1 || len(columns.VectorClock) != 1 {
		t.Errorf("the stored clock is %v, want the zero entry dropped so that the same causal "+
			"history is written one way rather than two", columns.VectorClock)
	}

	if columns.DeviceID == nil || *columns.DeviceID != phone {
		t.Error("the device whose write the row reflects was not stored")
	}
}

// The column is ON DELETE SET NULL, so a revision can lose the device it
// named, and the round trip has to survive it in both directions.
func TestADeviceThatCannotBeNamedIsNull(t *testing.T) {
	t.Parallel()

	columns := revision.ToColumns(crdt.Revision{})
	if columns.DeviceID != nil {
		t.Error("a revision naming no device was stored as one that does")
	}

	rebuilt := revision.FromColumns(nil, time.Time{}, nil, false)
	if rebuilt.DeviceID != (uuid.UUID{}) {
		t.Error("a NULL device came back as one")
	}
}

func TestFromColumnsRoundTrips(t *testing.T) {
	t.Parallel()

	phone := uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	original := crdt.FirstRevision(phone, at).Tombstone(phone, at.Add(time.Second))
	columns := revision.ToColumns(original)
	rebuilt := revision.FromColumns(columns.VectorClock, columns.UpdatedAt, columns.DeviceID, columns.Deleted)

	switch {
	case !rebuilt.VectorClock.Equal(original.VectorClock):
		t.Errorf("the causal history came back as %v, want %v", rebuilt.VectorClock, original.VectorClock)
	case !rebuilt.UpdatedAt.Equal(original.UpdatedAt):
		t.Errorf("the tie-break timestamp came back as %s", rebuilt.UpdatedAt)
	case rebuilt.DeviceID != original.DeviceID:
		t.Error("the tie-break lost its second half")
	case !rebuilt.Deleted:
		t.Error("a tombstone came back as a record the reader still has")
	}
}
