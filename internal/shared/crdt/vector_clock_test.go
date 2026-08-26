package crdt_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

const (
	phone  = crdt.DeviceID("phone")
	tablet = crdt.DeviceID("tablet")
	reader = crdt.DeviceID("reader")
)

func TestCompare(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		left  crdt.VectorClock
		right crdt.VectorClock
		want  crdt.Ordering
	}{
		"two empty clocks": {
			nil, nil, crdt.Equal,
		},
		"an empty clock against a populated one": {
			nil, crdt.VectorClock{phone: 1}, crdt.Before,
		},
		"the same history": {
			crdt.VectorClock{phone: 2, tablet: 1},
			crdt.VectorClock{phone: 2, tablet: 1},
			crdt.Equal,
		},
		"an absent device equals a device at zero": {
			crdt.VectorClock{phone: 2, tablet: 0},
			crdt.VectorClock{phone: 2},
			crdt.Equal,
		},
		"one device ahead": {
			crdt.VectorClock{phone: 1},
			crdt.VectorClock{phone: 2},
			crdt.Before,
		},
		"an extra device seen only by the right": {
			crdt.VectorClock{phone: 2},
			crdt.VectorClock{phone: 2, tablet: 1},
			crdt.Before,
		},
		"an extra device seen only by the left": {
			crdt.VectorClock{phone: 2, tablet: 1},
			crdt.VectorClock{phone: 2},
			crdt.After,
		},
		// The case reconciliation exists for: the tablet annotated offline
		// while the phone kept reading, and neither saw the other.
		"each ahead on its own device": {
			crdt.VectorClock{phone: 2, tablet: 1},
			crdt.VectorClock{phone: 1, tablet: 2},
			crdt.Concurrent,
		},
		"disjoint devices": {
			crdt.VectorClock{phone: 1},
			crdt.VectorClock{tablet: 1},
			crdt.Concurrent,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tt.left.Compare(tt.right); got != tt.want {
				t.Errorf("%s.Compare(%s) = %s, want %s", tt.left, tt.right, got, tt.want)
			}

			// Comparing from the other side must give the mirrored answer, or
			// two nodes looking at the same pair would disagree.
			if got, want := tt.right.Compare(tt.left), tt.want.Reverse(); got != want {
				t.Errorf("%s.Compare(%s) = %s, want %s", tt.right, tt.left, got, want)
			}
		})
	}
}

func TestPredicatesAgreeWithCompare(t *testing.T) {
	t.Parallel()

	before := crdt.VectorClock{phone: 1}
	after := crdt.VectorClock{phone: 2}
	concurrent := crdt.VectorClock{tablet: 1}

	if !before.HappensBefore(after) || before.HappensAfter(after) {
		t.Error("HappensBefore and HappensAfter disagree on a causally ordered pair")
	}

	if !before.IsConcurrentWith(concurrent) || before.Equal(concurrent) {
		t.Error("IsConcurrentWith and Equal disagree on a concurrent pair")
	}

	if !after.Dominates(before) || !after.Dominates(after) {
		t.Error("Dominates rejects a clock that has observed everything the other has")
	}

	if before.Dominates(after) || before.Dominates(concurrent) {
		t.Error("Dominates accepts a clock that is missing events")
	}
}

func TestTickAdvancesOnlyOneDevice(t *testing.T) {
	t.Parallel()

	original := crdt.VectorClock{phone: 2, tablet: 1}
	ticked := original.Tick(phone)

	if got, want := ticked.Get(phone), uint64(3); got != want {
		t.Errorf("phone = %d, want %d", got, want)
	}

	if got, want := ticked.Get(tablet), uint64(1); got != want {
		t.Errorf("tablet = %d, want %d", got, want)
	}

	// A write must be causally after the state it was made on.
	if !original.HappensBefore(ticked) {
		t.Errorf("%s does not happen before %s", original, ticked)
	}
}

func TestTickDoesNotMutateTheReceiver(t *testing.T) {
	t.Parallel()

	// A replicated structure that can be aliased by accident converges by
	// accident.
	original := crdt.VectorClock{phone: 2}
	original.Tick(phone)

	if got, want := original.Get(phone), uint64(2); got != want {
		t.Errorf("the receiver was mutated: phone = %d, want %d", got, want)
	}
}

func TestTickOnANilClock(t *testing.T) {
	t.Parallel()

	var empty crdt.VectorClock

	if got, want := empty.Tick(phone).Get(phone), uint64(1); got != want {
		t.Errorf("phone = %d, want %d", got, want)
	}
}

func TestMergeTakesThePointwiseMaximum(t *testing.T) {
	t.Parallel()

	left := crdt.VectorClock{phone: 2, tablet: 1}
	right := crdt.VectorClock{phone: 1, reader: 5}

	merged := left.Merge(right)

	for device, want := range map[crdt.DeviceID]uint64{phone: 2, tablet: 1, reader: 5} {
		if got := merged.Get(device); got != want {
			t.Errorf("%s = %d, want %d", device, got, want)
		}
	}
}

func TestMergeDominatesBothOperands(t *testing.T) {
	t.Parallel()

	left := crdt.VectorClock{phone: 2, tablet: 1}
	right := crdt.VectorClock{phone: 1, reader: 5}

	merged := left.Merge(right)

	if !merged.Dominates(left) || !merged.Dominates(right) {
		t.Errorf("%s does not dominate both %s and %s", merged, left, right)
	}
}

func TestMergeDoesNotMutateItsOperands(t *testing.T) {
	t.Parallel()

	left := crdt.VectorClock{phone: 2}
	right := crdt.VectorClock{tablet: 1}

	left.Merge(right)

	if left.Get(tablet) != 0 || right.Get(phone) != 0 {
		t.Errorf("Merge wrote into its operands: left=%s right=%s", left, right)
	}
}

func TestCompactDropsZeroEntries(t *testing.T) {
	t.Parallel()

	compacted := crdt.VectorClock{phone: 2, tablet: 0}.Compact()

	if _, present := compacted[tablet]; present {
		t.Errorf("Compact kept a zero entry: %v", compacted)
	}

	if got, want := compacted.Len(), 1; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	t.Parallel()

	original := crdt.VectorClock{phone: 2}
	clone := original.Clone()
	clone[phone] = 99

	if got, want := original.Get(phone), uint64(2); got != want {
		t.Errorf("writing to the clone changed the original: phone = %d, want %d", got, want)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := crdt.VectorClock{phone: 2, tablet: 1}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded crdt.VectorClock
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if !original.Equal(decoded) {
		t.Errorf("round trip changed the clock: %s became %s", original, decoded)
	}
}

func TestMarshalJSONIsCanonical(t *testing.T) {
	t.Parallel()

	// The same causal history must produce the same bytes however it was
	// built, so that two stored clocks can be compared without decoding.
	withZero := crdt.VectorClock{tablet: 1, phone: 2, reader: 0}
	without := crdt.VectorClock{phone: 2, tablet: 1}

	first, err := json.Marshal(withZero)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	second, err := json.Marshal(without)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("encodings differ: %s and %s", first, second)
	}

	if got, want := string(first), `{"phone":2,"tablet":1}`; got != want {
		t.Errorf("encoding = %s, want %s", got, want)
	}
}

func TestMarshalJSONRendersAnEmptyClockAsAnObject(t *testing.T) {
	t.Parallel()

	// A jsonb column should never have to tell null from an empty clock.
	var empty crdt.VectorClock

	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if got, want := string(encoded), "{}"; got != want {
		t.Errorf("encoding = %s, want %s", got, want)
	}
}

func TestUnmarshalJSONAcceptsNull(t *testing.T) {
	t.Parallel()

	var decoded crdt.VectorClock
	if err := json.Unmarshal([]byte("null"), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if !decoded.IsEmpty() {
		t.Errorf("null decoded to %s, want an empty clock", decoded)
	}
}

func TestUnmarshalJSONRejectsGarbage(t *testing.T) {
	t.Parallel()

	var decoded crdt.VectorClock
	if err := json.Unmarshal([]byte(`{"phone":"two"}`), &decoded); err == nil {
		t.Error("a non-numeric counter was accepted")
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	// Sorted, so that a log line or a failing test is diffable.
	if got, want := (crdt.VectorClock{tablet: 1, phone: 2}).String(), "{phone:2 tablet:1}"; got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}

	if got, want := crdt.VectorClock(nil).String(), "{}"; got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}
}

func TestOrderingReverse(t *testing.T) {
	t.Parallel()

	for order, want := range map[crdt.Ordering]crdt.Ordering{
		crdt.Before:     crdt.After,
		crdt.After:      crdt.Before,
		crdt.Equal:      crdt.Equal,
		crdt.Concurrent: crdt.Concurrent,
	} {
		if got := order.Reverse(); got != want {
			t.Errorf("%s.Reverse() = %s, want %s", order, got, want)
		}
	}
}

func TestOrderingString(t *testing.T) {
	t.Parallel()

	for order, want := range map[crdt.Ordering]string{
		crdt.Equal:      "equal",
		crdt.Before:     "before",
		crdt.After:      "after",
		crdt.Concurrent: "concurrent",
	} {
		if got := order.String(); got != want {
			t.Errorf("String() = %s, want %s", got, want)
		}
	}
}
