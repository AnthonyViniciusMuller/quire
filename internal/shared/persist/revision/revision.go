// Package revision converts between the causal state of a replicated record
// and the four columns that hold it.
//
// It is one package rather than a pair of helpers repeated in every repository
// that writes a replicated row, because the conversion carries a decision and a
// decision made in four places is a decision that drifts: device_id is
// nullable, and what a NULL there means has to be answered the same way for a
// work, a grouping, a filing and an annotation.
//
// It sits in the shared core rather than in the library slice, where it was
// written, for the same reason crdt.Revision does: the answer is about the
// causal state of a row and not about what the row holds, so it belongs to
// every slice that stores one — the library, the reading slice, and the sync
// reconciler that will read all of them.
//
// What it means is that the device is unknown. The column is
// ON DELETE SET NULL, so a revision can lose the device it named — in practice
// only when the reader themselves is deleted, since unbinding a device clears
// a flag and leaves the row. The tie-break then has one half instead of two,
// which is the right outcome: a record whose author no longer exists cannot be
// attributed to them, and the timestamp still orders it.
package revision

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// Columns is the four values a replicated row carries, in the form the
// generated parameters take them.
type Columns struct {
	// VectorClock is the causal version, compacted: zero entries are dropped,
	// so that the same causal history is stored one way rather than two.
	VectorClock crdt.VectorClock
	// UpdatedAt is the tie-break timestamp, on the clock C01 describes.
	UpdatedAt time.Time
	// DeviceID is the device whose write the row reflects, NULL when there is
	// none to name.
	DeviceID *uuid.UUID
	// Deleted is the tombstone.
	Deleted bool
}

// ToColumns renders a revision as the values a statement writes.
func ToColumns(rev crdt.Revision) Columns {
	columns := Columns{
		VectorClock: rev.VectorClock.Compact(),
		UpdatedAt:   rev.UpdatedAt,
		Deleted:     rev.Deleted,
	}

	if rev.DeviceID != (uuid.UUID{}) {
		device := rev.DeviceID
		columns.DeviceID = &device
	}

	return columns
}

// FromColumns rebuilds a revision from the values a row carries.
//
// The clock arrives already decoded and already canonical, because sqlc.yaml
// maps the column onto the type whose UnmarshalJSON drops the zero entries.
func FromColumns(clock crdt.VectorClock, updatedAt time.Time, device *uuid.UUID, deleted bool) crdt.Revision {
	rev := crdt.Revision{
		VectorClock: clock,
		UpdatedAt:   updatedAt,
		Deleted:     deleted,
	}

	if device != nil {
		rev.DeviceID = *device
	}

	return rev
}
