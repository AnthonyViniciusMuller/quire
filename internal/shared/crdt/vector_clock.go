// Package crdt holds the conflict-free replicated data type primitives the
// node reconciles with.
//
// A Quire device reads and annotates while disconnected, and so does every
// other device belonging to the same user, possibly registered on a different
// node. When they meet again there is no authority to ask which write came
// first: the answer has to be derivable from the data itself, identically on
// every node, without a coordinator.
//
// [VectorClock] is what makes that derivation possible. It records, for each
// device, how many events of that device a replica has observed. Comparing two
// clocks tells whether one write causally precedes the other or whether the
// two are genuinely concurrent — and only the concurrent case needs a
// tie-break, which is where the reconciler in internal/sync takes over.
//
// Clocks are values. No method mutates its receiver; each returns a new clock.
// A replicated structure that can be aliased by accident is a structure that
// converges by accident.
package crdt

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// DeviceID identifies the replica that authors events. It is the identity a
// vector clock entry is keyed by, which is why devices are registered before
// they may write.
type DeviceID string

// VectorClock counts, per device, the events this replica has observed from
// that device.
//
// A device absent from the map is indistinguishable from a device mapped to
// zero: both mean no event of that device has been observed. Every operation
// preserves that equivalence, so the same causal history always compares
// equal regardless of how it was built.
//
// The zero value, a nil map, is a valid empty clock and is safe to use.
type VectorClock map[DeviceID]uint64

// Get returns the number of events observed from device, which is zero for a
// device this clock has never seen.
func (vc VectorClock) Get(device DeviceID) uint64 { return vc[device] }

// Len returns the number of devices with at least one observed event.
func (vc VectorClock) Len() int {
	count := 0

	for _, counter := range vc {
		if counter > 0 {
			count++
		}
	}

	return count
}

// IsEmpty reports whether the clock has observed no event at all.
func (vc VectorClock) IsEmpty() bool { return vc.Len() == 0 }

// Clone returns an independent copy. Callers that hold a clock across a write
// need one, since a clock read from a repository may share storage with it.
func (vc VectorClock) Clone() VectorClock {
	if vc == nil {
		return nil
	}

	return maps.Clone(vc)
}

// Compact returns the clock without its zero entries, which is the canonical
// form used when persisting.
func (vc VectorClock) Compact() VectorClock {
	compacted := make(VectorClock, len(vc))

	for device, counter := range vc {
		if counter > 0 {
			compacted[device] = counter
		}
	}

	return compacted
}

// Tick returns a new clock with one more event observed from device. It is
// what a device calls when it authors a write.
//
// The counter is an event count on a single device; exhausting a uint64 would
// take longer than the lifetime of any library, so no overflow is handled.
func (vc VectorClock) Tick(device DeviceID) VectorClock {
	ticked := make(VectorClock, len(vc)+1)
	maps.Copy(ticked, vc)
	ticked[device] = vc[device] + 1

	return ticked
}

// Merge returns the least upper bound of the two clocks: for each device, the
// larger of the two counters.
//
// This is the join of the semilattice the whole design rests on. Being a
// pointwise maximum it is commutative, associative and idempotent, which is
// exactly what lets nodes exchange state in any order, more than once, and
// still converge.
func (vc VectorClock) Merge(other VectorClock) VectorClock {
	merged := make(VectorClock, max(len(vc), len(other)))

	for device, counter := range vc {
		if counter > 0 {
			merged[device] = counter
		}
	}

	for device, counter := range other {
		if counter > merged[device] {
			merged[device] = counter
		}
	}

	return merged
}

// Compare returns the causal relation between this clock and other.
//
// It walks both clocks once, recording whether any device is behind and
// whether any device is ahead. Behind and ahead at once is what concurrency
// means: each replica observed something the other did not.
func (vc VectorClock) Compare(other VectorClock) Ordering {
	var behind, ahead bool

	for device, counter := range vc {
		switch {
		case counter < other[device]:
			behind = true
		case counter > other[device]:
			ahead = true
		}
	}

	for device, counter := range other {
		// Devices present in both were already accounted for above.
		if _, seen := vc[device]; seen {
			continue
		}

		if counter > 0 {
			behind = true
		}
	}

	switch {
	case behind && ahead:
		return Concurrent
	case behind:
		return Before
	case ahead:
		return After
	default:
		return Equal
	}
}

// HappensBefore reports whether every event in this clock is also in other,
// and other holds at least one more.
func (vc VectorClock) HappensBefore(other VectorClock) bool {
	return vc.Compare(other) == Before
}

// HappensAfter is [VectorClock.HappensBefore] seen from the other side.
func (vc VectorClock) HappensAfter(other VectorClock) bool {
	return vc.Compare(other) == After
}

// IsConcurrentWith reports whether neither clock causally precedes the other,
// which is the case a reconciler has to break a tie for.
func (vc VectorClock) IsConcurrentWith(other VectorClock) bool {
	return vc.Compare(other) == Concurrent
}

// Equal reports whether both clocks describe the same causal history. A device
// mapped to zero and a device absent are the same history.
func (vc VectorClock) Equal(other VectorClock) bool {
	return vc.Compare(other) == Equal
}

// Dominates reports whether this clock has observed everything other has,
// whether or not it has observed more. It answers the question a replication
// worker asks: can this peer be skipped?
func (vc VectorClock) Dominates(other VectorClock) bool {
	switch vc.Compare(other) {
	case After, Equal:
		return true
	case Before, Concurrent:
		return false
	default:
		return false
	}
}

// MarshalJSON encodes the clock in its canonical form: zero entries dropped,
// and a nil clock rendered as an empty object rather than null, so that the
// jsonb column never has to distinguish the two.
//
// encoding/json sorts map keys, so the same causal history always produces the
// same bytes and two stored clocks can be compared without being decoded.
func (vc VectorClock) MarshalJSON() ([]byte, error) {
	// The conversion drops the method set, which is what stops this from
	// recursing into itself.
	encoded, err := json.Marshal(map[DeviceID]uint64(vc.Compact()))
	if err != nil {
		return nil, fmt.Errorf("crdt: encoding vector clock: %w", err)
	}

	return encoded, nil
}

// UnmarshalJSON decodes a clock, dropping zero entries so that what comes back
// from storage is in canonical form. A JSON null decodes to an empty clock.
func (vc *VectorClock) UnmarshalJSON(data []byte) error {
	var decoded map[DeviceID]uint64
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("crdt: decoding vector clock: %w", err)
	}

	*vc = VectorClock(decoded).Compact()

	return nil
}

// String renders the clock with its devices in a stable order, so that a log
// line or a failing test is readable and diffable.
func (vc VectorClock) String() string {
	devices := make([]DeviceID, 0, len(vc))

	for device, counter := range vc {
		if counter > 0 {
			devices = append(devices, device)
		}
	}

	slices.Sort(devices)

	entries := make([]string, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, fmt.Sprintf("%s:%d", device, vc[device]))
	}

	return "{" + strings.Join(entries, " ") + "}"
}
