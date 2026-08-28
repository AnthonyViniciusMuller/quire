package crdt_test

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// The laws below are about the join over whole revisions rather than over the
// clocks underneath them, and they are the ones eventual consistency (RNF03)
// actually rests on: two nodes reconcile records, not vector clocks.
//
// They hold only because the timestamp is stamped on a hybrid logical clock.
// The generator therefore stamps the way the node does — a replica observes
// what it merges, and every write it makes is at least one step past
// everything it has observed — and the last test in the file drops that
// discipline and shows the same reduction landing on two different answers,
// which is C01's counterexample and the reason the clock exists.

// epoch is where the simulated wall clocks start.
var epoch = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// replica is one device building revisions the way a node running the hybrid
// logical clock builds them.
type replica struct {
	device uuid.UUID
	clock  crdt.VectorClock
	seen   time.Time
}

// observe folds a revision this replica has received into what it knows.
func (p *replica) observe(revision crdt.Revision) {
	p.clock = p.clock.Merge(revision.VectorClock)

	if revision.UpdatedAt.After(p.seen) {
		p.seen = revision.UpdatedAt
	}
}

// write is one write by this replica, stamped at a wall clock that may be
// wrong. The floor is what makes the stamp causally monotonic anyway.
func (p *replica) write(wall time.Time) crdt.Revision {
	p.clock = p.clock.Tick(crdt.Author(p.device))

	at := wall.UTC().Truncate(crdt.Resolution)
	if floor := p.seen.Add(crdt.Resolution); at.Before(floor) {
		at = floor
	}

	p.seen = at

	return crdt.Revision{VectorClock: p.clock.Clone(), UpdatedAt: at, DeviceID: p.device}
}

// randomHistory builds size revisions of one record, written by a handful of
// replicas that sync with each other at random and whose wall clocks disagree
// by up to a minute in either direction.
func randomHistory(r *rand.Rand, size int) []crdt.Revision {
	replicas := make([]*replica, deviceCount)
	for index := range replicas {
		replicas[index] = &replica{device: uuid.New()}
	}

	history := make([]crdt.Revision, 0, size)

	for range size {
		author := replicas[r.IntN(len(replicas))]

		// The replica syncs with part of what already exists before writing,
		// which is what produces both causal chains and genuine concurrency.
		for _, revision := range history {
			if r.IntN(3) == 0 {
				author.observe(revision)
			}
		}

		skew := time.Duration(r.IntN(120)-60) * time.Second
		history = append(history, author.write(epoch.Add(skew)))
	}

	return history
}

// survivor reduces a history to the revision that survives merging all of it,
// in the order given.
func survivor(history []crdt.Revision) crdt.Revision {
	merged := history[0]
	for _, revision := range history[1:] {
		merged = merged.Merge(revision)
	}

	return merged
}

// equal reports whether two revisions are the same version of the record.
func equal(left, right crdt.Revision) bool {
	return left.VectorClock.Equal(right.VectorClock) &&
		left.UpdatedAt.Equal(right.UpdatedAt) &&
		left.DeviceID == right.DeviceID &&
		left.Deleted == right.Deleted
}

func TestRevisionMergeIsCommutative(t *testing.T) {
	t.Parallel()

	// Two nodes reconciling the same pair of versions must reach the same
	// record, whichever of them received the other's.
	r := newRand(t)

	for range iterations {
		history := randomHistory(r, 2)
		left, right := history[0], history[1]

		if !equal(left.Merge(right), right.Merge(left)) {
			t.Fatalf("merge is not commutative: %+v and %+v", left, right)
		}
	}
}

func TestRevisionMergeIsAssociative(t *testing.T) {
	t.Parallel()

	// A node may receive three versions in one batch or in three, grouped any
	// way. The grouping must not change which one it keeps.
	r := newRand(t)

	for range iterations {
		history := randomHistory(r, 3)
		first, second, third := history[0], history[1], history[2]

		left := first.Merge(second).Merge(third)
		right := first.Merge(second.Merge(third))

		if !equal(left, right) {
			t.Fatalf("merge is not associative: %+v, %+v, %+v", first, second, third)
		}
	}
}

func TestRevisionMergeIsIdempotent(t *testing.T) {
	t.Parallel()

	// Replication retries, and an operation redelivered must be a no-op. This
	// is what makes at-least-once delivery sufficient.
	r := newRand(t)

	for range iterations {
		revision := randomHistory(r, 1)[0]

		if !equal(revision.Merge(revision), revision) {
			t.Fatalf("merge is not idempotent for %+v", revision)
		}
	}
}

// The order and the grouping in which a node happened to receive a record's
// versions must not change where it lands. This is the property the whole of
// RNF03 is, over a history rather than over a pair.
func TestReducingAHistoryIsIndependentOfTheOrderItArrivedIn(t *testing.T) {
	t.Parallel()

	r := newRand(t)

	for range iterations / 4 {
		history := randomHistory(r, 6)
		want := survivor(history)

		for range 8 {
			shuffled := slices.Clone(history)
			r.Shuffle(len(shuffled), func(i, j int) {
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			})

			if got := survivor(shuffled); !equal(got, want) {
				t.Fatalf("the same history reduced to %+v and to %+v", want, got)
			}
		}
	}
}

// A maximum exists only if the relation has no cycle, and a strict total order
// is exactly the statement that it has none. Any two distinct versions must
// stand in it one way or the other, and never both.
func TestSupersedesIsAStrictTotalOrder(t *testing.T) {
	t.Parallel()

	r := newRand(t)

	for range iterations {
		history := randomHistory(r, 3)
		first, second, third := history[0], history[1], history[2]

		if first.Supersedes(first) {
			t.Fatalf("%+v supersedes itself", first)
		}

		if first.Supersedes(second) && second.Supersedes(first) {
			t.Fatalf("%+v and %+v supersede each other", first, second)
		}

		if !equal(first, second) && !first.Supersedes(second) && !second.Supersedes(first) {
			t.Fatalf("neither of %+v and %+v supersedes the other", first, second)
		}

		if first.Supersedes(second) && second.Supersedes(third) && !first.Supersedes(third) {
			t.Fatalf("the order is not transitive: %+v, %+v, %+v", first, second, third)
		}
	}
}

// Two devices that have never heard of each other can stamp the same
// microsecond, and something has to settle them. Any fixed rule works provided
// every node applies the same one.
func TestSupersedesSettlesAnEqualInstantOnTheDevice(t *testing.T) {
	t.Parallel()

	at := epoch
	low, high := uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		uuid.MustParse("ffffffff-0000-4000-8000-000000000001")

	left := crdt.Revision{VectorClock: crdt.VectorClock{crdt.Author(low): 1}, UpdatedAt: at, DeviceID: low}
	right := crdt.Revision{VectorClock: crdt.VectorClock{crdt.Author(high): 1}, UpdatedAt: at, DeviceID: high}

	if !left.VectorClock.IsConcurrentWith(right.VectorClock) {
		t.Fatal("the two versions are not concurrent, so the tie-break is not what is being tested")
	}

	if !right.Supersedes(left) || left.Supersedes(right) {
		t.Error("the tie on the instant was not settled on the device, or was settled both ways")
	}
}

// The causal order decides first, and decides alone: a version that observed
// another wins whatever the two instants say, which is what stops a device
// with a fast clock from beating a write it has already seen.
func TestSupersedesConsultsTheClockBeforeTheInstant(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	earlier := crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 1},
		UpdatedAt:   epoch.Add(time.Hour),
		DeviceID:    phone,
	}
	later := crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 2},
		UpdatedAt:   epoch,
		DeviceID:    phone,
	}

	if !later.Supersedes(earlier) {
		t.Error("a causally later version lost to the instant, which the clock should have decided")
	}
}

// C01's counterexample, run as the correction states it.
//
// Three writes to one record. The tablet's clock is ten seconds behind and it
// has seen the phone's write, so with a wall clock in the timestamp its
// instant is earlier than the write it causally follows. That one edge makes
// the relation cyclic, and the test below is what a cycle costs: the same
// three versions reduce to two different survivors according to the order they
// arrived in. Nothing detects it — each node applied the rule correctly.
func TestAWallClockTimestampMakesTheReductionOrderDependent(t *testing.T) {
	t.Parallel()

	phone, tablet, laptop := uuid.New(), uuid.New(), uuid.New()

	// a: the phone writes, its clock correct.
	a := crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 1},
		UpdatedAt:   epoch.Add(5 * time.Second),
		DeviceID:    phone,
	}

	// b: the tablet has synced and seen a, and writes with a clock ten seconds
	// behind. This is the bad edge — b follows a and is stamped before it.
	b := crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 1, crdt.Author(tablet): 1},
		UpdatedAt:   epoch.Add(-2 * time.Second),
		DeviceID:    tablet,
	}

	// c: the laptop has synced with nobody, and its clock is correct.
	c := crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(laptop): 1},
		UpdatedAt:   epoch.Add(2 * time.Second),
		DeviceID:    laptop,
	}

	nodeX := survivor([]crdt.Revision{a, b, c})
	nodeY := survivor([]crdt.Revision{b, c, a})

	if equal(nodeX, nodeY) {
		t.Fatal("the counterexample no longer diverges, so it is no longer the counterexample")
	}

	// And it is the bad edge that does it: with b stamped after the a it
	// follows — which is what the hybrid logical clock guarantees — the two
	// nodes agree again.
	b.UpdatedAt = a.UpdatedAt.Add(crdt.Resolution)

	if !equal(survivor([]crdt.Revision{a, b, c}), survivor([]crdt.Revision{b, c, a})) {
		t.Error("the two nodes still disagree once the causally later write carries the later instant")
	}
}

// A record with one writer has nothing to tie: its versions are totally
// ordered by that writer's own counter, and the join is the later count (C05).
func TestVersionMergeTakesTheLaterCount(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	first := crdt.FirstVersion(phone, epoch)
	second := first.Next(phone, epoch.Add(-time.Hour))

	if !second.Supersedes(first) || first.Supersedes(second) {
		t.Error("the later version did not win, or both did")
	}

	if merged := first.Merge(second); !merged.VectorClock.Equal(second.VectorClock) {
		t.Errorf("Merge = %s, want the later count", merged.VectorClock)
	}

	// Two versions of a row whose only writer is the device the row names
	// cannot be concurrent. One that is describes a row written by a device
	// with no business writing it, and neither side wins it.
	stranger := crdt.FirstVersion(uuid.New(), epoch)

	if first.Supersedes(stranger) || stranger.Supersedes(first) {
		t.Error("a concurrent pair of single-writer versions was settled, and there is no rule to settle it by")
	}
}
