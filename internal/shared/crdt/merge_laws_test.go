package crdt_test

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// The merge laws are what make offline reconciliation work at all. Nodes
// exchange state in whatever order the network allows, more than once, and in
// batches grouped differently on each side. Convergence is guaranteed only
// because the join is commutative, associative and idempotent: those three
// properties are precisely the statement that the outcome does not depend on
// the order, the grouping, or the number of times state was exchanged.
//
// The tests below check them over randomly generated clocks rather than over a
// handful of chosen examples, and are the evidence behind section 4.3.3 of the
// thesis.

const (
	// iterations is how many random cases each law is checked against.
	iterations = 2000
	// seed fixes the generator, so a failure is reproducible and CI does not
	// flake. Change it to explore a different corner of the space.
	seed = 0x5175697265 // "Quire"
	// devices is drawn from a small alphabet on purpose: clocks must overlap
	// often, or concurrency would almost never be generated.
	deviceCount = 4
	// maxCounter keeps counters low for the same reason.
	maxCounter = 5
)

// newRand returns the generator for one property, seeded deterministically.
func newRand(t *testing.T) *rand.Rand {
	t.Helper()

	return rand.New(rand.NewPCG(seed, uint64(len(t.Name()))))
}

// randomClock builds a clock over a small device alphabet, including the
// empty clock and clocks with explicit zero entries.
func randomClock(r *rand.Rand) crdt.VectorClock {
	clock := make(crdt.VectorClock)

	for device := range deviceCount {
		if r.IntN(3) == 0 {
			continue
		}

		clock[crdt.DeviceID(string(rune('a'+device)))] = uint64(r.IntN(maxCounter))
	}

	return clock
}

// randomChain builds three clocks in causal order, each derived from the last
// by a few ticks. Random triples almost never happen to be causally ordered,
// so the properties about happens-before need histories built on purpose.
func randomChain(r *rand.Rand) (first, second, third crdt.VectorClock) {
	first = randomClock(r)

	second = first
	for range 1 + r.IntN(3) {
		second = second.Tick(crdt.DeviceID(string(rune('a' + r.IntN(deviceCount)))))
	}

	third = second
	for range 1 + r.IntN(3) {
		third = third.Tick(crdt.DeviceID(string(rune('a' + r.IntN(deviceCount)))))
	}

	return first, second, third
}

func TestMergeIsCommutative(t *testing.T) {
	t.Parallel()

	// Two nodes reconciling the same pair of states must reach the same
	// result, whichever of them received the other's state.
	r := newRand(t)

	for range iterations {
		left, right := randomClock(r), randomClock(r)

		if !left.Merge(right).Equal(right.Merge(left)) {
			t.Fatalf("merge is not commutative for %s and %s: %s against %s",
				left, right, left.Merge(right), right.Merge(left))
		}
	}
}

func TestMergeIsAssociative(t *testing.T) {
	t.Parallel()

	// A node may receive three states in one batch or in three, grouped any
	// way. The grouping must not change where it lands.
	r := newRand(t)

	for range iterations {
		first, second, third := randomClock(r), randomClock(r), randomClock(r)

		left := first.Merge(second).Merge(third)
		right := first.Merge(second.Merge(third))

		if !left.Equal(right) {
			t.Fatalf("merge is not associative for %s, %s and %s: %s against %s",
				first, second, third, left, right)
		}
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	t.Parallel()

	// Replication retries, and a redelivered operation must be a no-op. This
	// is what makes at-least-once delivery sufficient.
	r := newRand(t)

	for range iterations {
		clock := randomClock(r)

		if !clock.Merge(clock).Equal(clock) {
			t.Fatalf("merge is not idempotent for %s: %s", clock, clock.Merge(clock))
		}
	}
}

func TestMergeHasTheEmptyClockAsIdentity(t *testing.T) {
	t.Parallel()

	// A device that has never written must not perturb what it merges into.
	r := newRand(t)

	for range iterations {
		clock := randomClock(r)

		if !clock.Merge(nil).Equal(clock) {
			t.Fatalf("merging the empty clock into %s gave %s", clock, clock.Merge(nil))
		}
	}
}

func TestMergeIsTheLeastUpperBound(t *testing.T) {
	t.Parallel()

	// The result must dominate both operands, and must add nothing neither of
	// them had: an over-eager join would make a node claim to have seen events
	// it never received.
	r := newRand(t)

	for range iterations {
		left, right := randomClock(r), randomClock(r)
		merged := left.Merge(right)

		if !merged.Dominates(left) || !merged.Dominates(right) {
			t.Fatalf("%s does not dominate both %s and %s", merged, left, right)
		}

		for device, counter := range merged {
			if counter != max(left.Get(device), right.Get(device)) {
				t.Fatalf("%s invented events for %s: merging %s and %s", merged, device, left, right)
			}
		}
	}
}

func TestMergeAbsorbsAnAlreadyDominatedClock(t *testing.T) {
	t.Parallel()

	// If a peer's state is already covered, merging it changes nothing. This
	// is the property the replication worker relies on to skip a peer.
	r := newRand(t)

	for range iterations {
		older, newer, _ := randomChain(r)

		if !older.Merge(newer).Equal(newer) {
			t.Fatalf("merging %s into the later %s gave %s", older, newer, older.Merge(newer))
		}
	}
}

func TestCompareIsAntisymmetric(t *testing.T) {
	t.Parallel()

	// Two nodes looking at the same pair must not disagree about which came
	// first, or reconciliation would be direction-dependent.
	r := newRand(t)

	for range iterations {
		left, right := randomClock(r), randomClock(r)

		if got, want := right.Compare(left), left.Compare(right).Reverse(); got != want {
			t.Fatalf("%s.Compare(%s) = %s, but the reverse comparison says %s",
				right, left, got, want)
		}
	}
}

func TestCompareAgreesWithMerge(t *testing.T) {
	t.Parallel()

	// The causal order and the lattice order have to be the same order:
	// a happens before b exactly when joining a into b changes nothing and the
	// two are not already equal.
	r := newRand(t)

	for range iterations {
		left, right := randomClock(r), randomClock(r)

		absorbed := left.Merge(right).Equal(right) && !left.Equal(right)

		if before := left.HappensBefore(right); before != absorbed {
			t.Fatalf("%s.HappensBefore(%s) = %t, but the join says %t",
				left, right, before, absorbed)
		}
	}
}

func TestHappensBeforeIsTransitive(t *testing.T) {
	t.Parallel()

	// Without transitivity the causal order is not an order, and the whole
	// reconciliation argument collapses.
	r := newRand(t)

	for range iterations {
		first, second, third := randomChain(r)

		if !first.HappensBefore(second) || !second.HappensBefore(third) {
			t.Fatalf("the generated chain is not causal: %s, %s, %s", first, second, third)
		}

		if !first.HappensBefore(third) {
			t.Fatalf("%s happens before %s and %s before %s, but not %s before %s",
				first, second, second, third, first, third)
		}
	}
}

func TestExactlyOneRelationHolds(t *testing.T) {
	t.Parallel()

	// The four orderings must partition every pair. A pair matching none, or
	// more than one, would leave the reconciler without a rule to apply.
	r := newRand(t)

	for range iterations {
		left, right := randomClock(r), randomClock(r)

		holding := 0

		for _, holds := range []bool{
			left.Equal(right),
			left.HappensBefore(right),
			left.HappensAfter(right),
			left.IsConcurrentWith(right),
		} {
			if holds {
				holding++
			}
		}

		if holding != 1 {
			t.Fatalf("%d relations hold between %s and %s, want exactly 1", holding, left, right)
		}
	}
}

func TestTickIsCausallyAfterTheClockItAdvances(t *testing.T) {
	t.Parallel()

	// A write must be causally after the state it was made on, or it could be
	// discarded as stale by the node that receives it.
	r := newRand(t)

	for range iterations {
		clock := randomClock(r)
		device := crdt.DeviceID(string(rune('a' + r.IntN(deviceCount))))
		ticked := clock.Tick(device)

		if !clock.HappensBefore(ticked) {
			t.Fatalf("%s does not happen before %s, its own tick of %s", clock, ticked, device)
		}

		if !ticked.Merge(clock).Equal(ticked) {
			t.Fatalf("merging %s back into its tick %s changed it", clock, ticked)
		}
	}
}

func TestOperationsDoNotMutateTheirOperands(t *testing.T) {
	t.Parallel()

	// Value semantics are what stop one aggregate's clock from being advanced
	// by a merge performed somewhere else entirely.
	r := newRand(t)

	for range iterations {
		left, right := randomClock(r), randomClock(r)
		beforeLeft, beforeRight := left.String(), right.String()

		left.Merge(right)
		left.Compare(right)
		left.Tick("a")
		left.Compact()

		if left.String() != beforeLeft || right.String() != beforeRight {
			t.Fatalf("an operand was mutated: %s became %s, %s became %s",
				beforeLeft, left, beforeRight, right)
		}
	}
}

func TestJSONRoundTripPreservesTheHistory(t *testing.T) {
	t.Parallel()

	r := newRand(t)

	for range iterations {
		original := randomClock(r)

		encoded, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", original, err)
		}

		var decoded crdt.VectorClock
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", encoded, err)
		}

		if !original.Equal(decoded) {
			t.Fatalf("round trip changed %s into %s", original, decoded)
		}
	}
}

func TestEqualClocksEncodeToEqualBytes(t *testing.T) {
	t.Parallel()

	// Canonical encoding is what lets the database compare two stored clocks
	// without decoding them.
	r := newRand(t)

	for range iterations {
		clock := randomClock(r)

		// The same history, reached by merging in a clock it already
		// dominates, and therefore possibly carrying different zero entries.
		equivalent := clock.Merge(clock.Compact())

		if !clock.Equal(equivalent) {
			t.Fatalf("the generated pair is not equal: %s and %s", clock, equivalent)
		}

		first, err := json.Marshal(clock)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", clock, err)
		}

		second, err := json.Marshal(equivalent)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", equivalent, err)
		}

		if !bytes.Equal(first, second) {
			t.Fatalf("equal clocks encoded differently: %s and %s", first, second)
		}
	}
}
