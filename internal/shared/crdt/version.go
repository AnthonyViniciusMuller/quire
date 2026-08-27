package crdt

import (
	"time"
	"uuid"
)

// Version is the replication metadata of a record that has exactly one
// writer: the causal version, and the instant the write was stamped at.
//
// Reading progress is the only such record, and C05 in
// docs/tcc-corrections.md is why it exists as a type of its own. A progress
// row belongs to one work and one device, so that device is its only writer,
// its writes are totally ordered by its own counter, and two versions of the
// row can never be concurrent. There is nothing to break a tie between,
// which is precisely the two fields [Revision] carries and this does not:
// the device whose write the record reflects is already the row's own key,
// and a tombstone would be a deletion that has no meaning — a reader who
// stops reading a work leaves the position where it was.
//
// It is therefore not a smaller [Revision] and does not embed one. The clock
// here is a version counter for deduplication during replication rather than
// a conflict resolver, and a type that offered a tie-break would invite the
// reconciler to apply one where nothing ties.
//
// What the two share is the rule that matters: the timestamp is stamped by
// the same [stamp], so a causally later version of a row can never carry an
// earlier instant than the version it was derived from, whichever of the two
// kinds of record it is.
type Version struct {
	// VectorClock is the causal version of the record. Because the row has
	// one writer, it always has exactly one entry, and it grows by one on
	// every write.
	VectorClock VectorClock

	// UpdatedAt is when the write happened, on the clock C01 describes: it is
	// causally monotonic, not a reading of the wall clock.
	UpdatedAt time.Time
}

// FirstVersion is the metadata of a record device has just written for the
// first time.
func FirstVersion(device uuid.UUID, at time.Time) Version {
	return Version{
		VectorClock: VectorClock{}.Tick(Author(device)),
		UpdatedAt:   stamp(at, time.Time{}),
	}
}

// Next is the metadata of the record after its device writes to it again.
//
// The device is a parameter rather than something the version remembers,
// because the version does not carry one: which device this is belongs to the
// row, and passing it here is what keeps the clock keyed by the same device
// that a caller could otherwise silently change.
func (v Version) Next(device uuid.UUID, at time.Time) Version {
	return Version{
		VectorClock: v.VectorClock.Tick(Author(device)),
		UpdatedAt:   stamp(at, v.UpdatedAt),
	}
}

// IsZero reports whether the record has no causal history at all, which is the
// state of a value nothing has stamped.
func (v Version) IsZero() bool { return v.VectorClock.IsEmpty() && v.UpdatedAt.IsZero() }
