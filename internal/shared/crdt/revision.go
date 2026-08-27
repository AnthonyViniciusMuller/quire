package crdt

import (
	"time"
	"uuid"
)

// Resolution is the granularity every replicated timestamp is held at.
//
// It is a microsecond because that is what a PostgreSQL timestamptz stores. A
// value stamped at a finer granularity would not survive a write followed by a
// read, and two writes the database cannot tell apart would compare unequal in
// memory and equal on disk — which is the sort of difference a reconciler
// decides a conflict by.
const Resolution = time.Microsecond

// Revision is the replication metadata every replicable record carries: the
// e-book, the collection, the membership of one in the other, and the
// annotation. It is the same four fields on all of them, which is what lets
// one reconciler serve every entity.
//
// Reading progress is the exception, and C05 in docs/tcc-corrections.md
// explains why: a progress row has exactly one writer, so its versions can
// never be concurrent and it carries the clock and the timestamp without the
// two fields that break a tie.
//
// It lives here rather than in a slice because three slices hold one. The
// library slice writes it, the reading slice writes it, and the sync slice
// reconciles it — and a copy per slice would be three definitions of the
// causal state of one row.
type Revision struct {
	// VectorClock is the causal version of the record. A reconciler consults
	// this first, and consults nothing else unless it reports the two versions
	// concurrent.
	VectorClock VectorClock

	// UpdatedAt is the first half of the tie-break for the concurrent case,
	// on the clock described by C01: it is causally monotonic, not a wall
	// clock. [Revision.Next] is what keeps it so.
	UpdatedAt time.Time

	// DeviceID is the second half, and the reason the pair is a total order:
	// two devices that have never heard of each other can still land on the
	// same timestamp, and any fixed rule settles them provided every node
	// applies the same one.
	//
	// It is the device whose write the record currently reflects, never the
	// one that created it. Appendix A of the TCC describes it the other way
	// round on anotacao; see C10.
	DeviceID uuid.UUID

	// Deleted is the tombstone. A record removed outright is resurrected by
	// the next node that had not yet heard about the deletion, so deletion is
	// a write like any other and reconciles like one.
	Deleted bool
}

// Author renders a device identifier as the key a vector clock entry is
// counted under.
//
// The clock is keyed by a string because that is what a jsonb object holds,
// and the identifier is a uuid because that is what identity.devices declares.
// One conversion, in one place, is what keeps the two spellings from drifting.
func Author(device uuid.UUID) DeviceID { return DeviceID(device.String()) }

// FirstRevision is the metadata of a record device has just created: one
// event, observed from device alone.
func FirstRevision(device uuid.UUID, at time.Time) Revision {
	return Revision{
		VectorClock: VectorClock{}.Tick(Author(device)),
		UpdatedAt:   stamp(at, time.Time{}),
		DeviceID:    device,
	}
}

// Next is the metadata of the record after device writes to it again.
//
// Three things happen, and each is required by a different part of the design.
// The clock ticks, so that the new version causally dominates the one it was
// derived from. The timestamp is stamped, never merely assigned. And the
// device is recorded as the one whose write the record now reflects, which is
// what C10 corrects in Appendix A.
//
// The tombstone is deliberately not cleared. A record is undeleted by a write
// that says so, not by any write that happens to follow the deletion.
func (r Revision) Next(device uuid.UUID, at time.Time) Revision {
	return Revision{
		VectorClock: r.VectorClock.Tick(Author(device)),
		UpdatedAt:   stamp(at, r.UpdatedAt),
		DeviceID:    device,
		Deleted:     r.Deleted,
	}
}

// Tombstone is [Revision.Next] with the record marked removed. Deletion is a
// write, and reconciles like one.
func (r Revision) Tombstone(device uuid.UUID, at time.Time) Revision {
	next := r.Next(device, at)
	next.Deleted = true

	return next
}

// Restore is [Revision.Next] with the record marked present again, which is
// what an offline device re-adding something another device removed is asking
// for.
func (r Revision) Restore(device uuid.UUID, at time.Time) Revision {
	next := r.Next(device, at)
	next.Deleted = false

	return next
}

// IsZero reports whether the record has no causal history at all, which is the
// state of a value nothing has stamped.
func (r Revision) IsZero() bool {
	return r.VectorClock.IsEmpty() && r.UpdatedAt.IsZero() && r.DeviceID == (uuid.UUID{}) && !r.Deleted
}

// stamp returns the instant to record, given the wall clock reading and the
// instant the previous version carries.
//
// It is the local half of C01. The rule the correction states is that a write
// stamps max(local wall clock, greatest observed timestamp + one step), and
// what is applied here is that rule over one record: a second write to the
// same row is later than the first even when the authoring device's clock ran
// backwards between them, so a causally later version can never carry an
// earlier timestamp and the tie-break cannot cycle.
//
// The other half is node-wide, over every timestamp the replica has observed
// rather than over one row, and it belongs to the hybrid logical clock of
// phase 9. The two compose: a maximum of maxima is a maximum, so the clock
// replacing the wall clock reading here strengthens this without changing it.
//
// The truncation is what makes the value survive a round trip through a
// timestamptz, and what makes "one step" mean the smallest difference the
// column can still represent.
func stamp(at, previous time.Time) time.Time {
	stamped := at.UTC().Truncate(Resolution)

	if floor := previous.UTC().Truncate(Resolution).Add(Resolution); stamped.Before(floor) {
		stamped = floor
	}

	return stamped
}
