// Package crdtpb renders the causal metadata of a replicated record in the
// form the contract carries it.
//
// It is one package rather than a function in each slice's convert, because
// what it renders is one thing: three slices store a [crdt.Revision] — the
// library writes it, the reading slice writes it, the sync slice reconciles it
// — and every one of them has to put the same value on the wire the same way.
// The rule that would drift is the compaction: a device absent from a clock and
// a device mapped to zero are the same causal history, and a slice that sent
// both forms would make two equal histories compare unequal on the receiving
// node.
//
// It goes one way only. Nothing here reads a message back into a revision,
// because no client of this contract may send one: the server stamps it, and a
// client that could send its own could claim to have written before a write it
// has already seen. What comes the other way is the sync slice's business, and
// it arrives as an operation rather than as a rendered record.
//
// It sits beside crdt rather than inside it because the domain must not know
// that a protobuf exists — the direction is the whole point of a convert
// package, and putting the dependency here keeps internal/shared/crdt free of
// it.
package crdtpb

import (
	"time"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// VectorClock renders a causal version.
//
// Zero entries are dropped, as the contract requires. The result is never nil:
// an empty clock is an empty map, so a client that ranges over the entries
// behaves the same on a record nothing has written twice.
func VectorClock(clock crdt.VectorClock) *quirev1.VectorClock {
	rendered := &quirev1.VectorClock{Entries: make(map[string]uint64, clock.Len())}

	for device, counter := range clock.Compact() {
		rendered.Entries[string(device)] = counter
	}

	return rendered
}

// Timestamp renders a causal instant.
//
// It is a HybridTimestamp and not a google.protobuf.Timestamp, and the contract
// is emphatic about why: the value is not a wall clock, and a distinct message
// is what stops anything from comparing it against one by accident or rendering
// it to a reader as the time of day (C01 in docs/tcc-corrections.md).
//
// The unit is the microsecond, which is what a timestamptz stores and therefore
// what the value has already been truncated to.
func Timestamp(at time.Time) *quirev1.HybridTimestamp {
	return &quirev1.HybridTimestamp{UnixMicros: at.UnixMicro()}
}

// Revision renders the replication metadata of a record that any device may
// write: the clock, the tie-break, and the tombstone.
func Revision(revision crdt.Revision) *quirev1.Revision {
	return &quirev1.Revision{
		VectorClock: VectorClock(revision.VectorClock),
		UpdatedAt:   Timestamp(revision.UpdatedAt),
		DeviceId:    revision.DeviceID.String(),
		Deleted:     revision.Deleted,
	}
}
