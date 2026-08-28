package hlc_test

import (
	"sync"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
)

// noon is the instant the wall clocks below are set around.
var noon = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixed returns a wall clock stuck at at, and a handle to move it.
func fixed(at time.Time) (func() time.Time, *time.Time) {
	reading := at

	return func() time.Time { return reading }, &reading
}

func TestNowReadsTheWallClock(t *testing.T) {
	t.Parallel()

	wall, _ := fixed(noon)
	clock := hlc.New(hlc.WithWallClock(wall))

	if stamped := clock.Now(); !stamped.Equal(noon) {
		t.Errorf("Now = %s, want the wall clock's %s", stamped, noon)
	}
}

// The instant travels: it is written to a timestamptz, rendered onto the wire
// as a hybrid timestamp, and compared against instants stamped elsewhere. A
// value the column cannot hold would not survive a write and a read back.
func TestNowIsUTCAtTheResolutionTheColumnKeeps(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("BRT", -3*60*60)
	wall, _ := fixed(noon.In(zone).Add(750 * time.Nanosecond))
	clock := hlc.New(hlc.WithWallClock(wall))

	stamped := clock.Now()

	if stamped.Location() != time.UTC {
		t.Errorf("Now stamped in %s", stamped.Location())
	}

	if stamped.Truncate(crdt.Resolution) != stamped {
		t.Errorf("Now = %s, want it at the microsecond the column keeps", stamped)
	}
}

// This is the whole of what the type is for. A wall clock that runs backwards
// between two writes is what makes the last-writer-wins relation cyclic in
// C01; the clock steps forward instead.
func TestNowIsStrictlyIncreasingWhileTheWallClockRunsBackwards(t *testing.T) {
	t.Parallel()

	wall, reading := fixed(noon)
	clock := hlc.New(hlc.WithWallClock(wall))

	first := clock.Now()

	*reading = noon.Add(-10 * time.Second)

	second := clock.Now()
	third := clock.Now()

	switch {
	case !second.After(first):
		t.Errorf("the second write stamped %s, which is not after the first's %s", second, first)
	case !third.After(second):
		t.Errorf("the third write stamped %s, which is not after the second's %s", third, second)
	case second.Sub(first) != crdt.Resolution:
		t.Errorf("the clock stepped by %s, want one step of %s", second.Sub(first), crdt.Resolution)
	}
}

// Two writes racing on two connections must not carry the same instant, or the
// tie-break would fall through to the device on a pair this node could have
// ordered itself.
func TestNowNeverRepeatsUnderConcurrency(t *testing.T) {
	t.Parallel()

	wall, _ := fixed(noon)
	clock := hlc.New(hlc.WithWallClock(wall))

	const writers = 64

	stamped := make([]time.Time, writers)

	var group sync.WaitGroup

	group.Add(writers)

	for writer := range writers {
		go func() {
			defer group.Done()

			stamped[writer] = clock.Now()
		}()
	}

	group.Wait()

	seen := make(map[time.Time]struct{}, writers)
	for _, at := range stamped {
		if _, repeated := seen[at]; repeated {
			t.Fatalf("two writes were stamped %s", at)
		}

		seen[at] = struct{}{}
	}
}

// A local write that follows a remote one must be stamped after it, and the
// only way this node can know what "after" is is to have been told.
func TestObserveRaisesTheFloorAWriteIsStampedAbove(t *testing.T) {
	t.Parallel()

	wall, _ := fixed(noon)
	clock := hlc.New(hlc.WithWallClock(wall))

	peer := noon.Add(30 * time.Second)

	if !clock.Observe(peer) {
		t.Fatalf("an instant %s ahead was refused", peer.Sub(noon))
	}

	if observed := clock.Observed(); !observed.Equal(peer) {
		t.Errorf("Observed = %s, want the peer's %s", observed, peer)
	}

	if stamped := clock.Now(); !stamped.After(peer) {
		t.Errorf("the next write stamped %s, which is not after the peer's %s", stamped, peer)
	}
}

// Observing is a maximum, so an instant already covered changes nothing —
// which is what lets the reconciler observe every operation of a batch without
// caring what order they arrived in.
func TestObserveNeverMovesTheClockBackwards(t *testing.T) {
	t.Parallel()

	wall, _ := fixed(noon)
	clock := hlc.New(hlc.WithWallClock(wall))

	stamped := clock.Now()

	for _, at := range []time.Time{noon.Add(-time.Hour), noon, {}} {
		clock.Observe(at)

		if observed := clock.Observed(); !observed.Equal(stamped) {
			t.Errorf("observing %s moved the clock to %s, want it left at %s", at, observed, stamped)
		}
	}
}

// One peer whose clock is a year fast would otherwise push this node a year
// into the future and keep it there, and every tie against a correctly stamped
// write would go to whatever it had poisoned.
func TestObserveRefusesAnInstantBeyondTheDriftCeiling(t *testing.T) {
	t.Parallel()

	wall, _ := fixed(noon)
	clock := hlc.New(hlc.WithWallClock(wall))

	if clock.Observe(noon.Add(hlc.MaxDrift + time.Minute)) {
		t.Error("an instant beyond the ceiling was adopted")
	}

	if observed := clock.Observed(); !observed.IsZero() {
		t.Errorf("the clock followed it to %s", observed)
	}

	// Just inside the ceiling is an ordinary observation: the margin is for
	// machines that are trying to keep time.
	if !clock.Observe(noon.Add(hlc.MaxDrift - time.Minute)) {
		t.Error("an instant inside the ceiling was refused")
	}
}

// The counterexample of C01, run against this clock.
//
// Three devices write to one record. The tablet's wall clock is ten seconds
// behind and it has already seen the phone's write, so with a plain wall clock
// its instant is earlier than the write it causally follows — which is the one
// bad edge, and the reason the relation has a cycle and merge stops being
// associative. Here the tablet is a replica that observed the phone's write
// before making its own.
func TestTheCounterexampleOfC01HasNoBadEdge(t *testing.T) {
	t.Parallel()

	// The phone's clock is correct.
	phoneWall, _ := fixed(noon.Add(5 * time.Second))
	phone := hlc.New(hlc.WithWallClock(phoneWall))

	// The tablet's is ten seconds behind.
	tabletWall, _ := fixed(noon.Add(-2 * time.Second))
	tablet := hlc.New(hlc.WithWallClock(tabletWall))

	// The laptop's is correct and it has synced with nobody.
	laptopWall, _ := fixed(noon.Add(2 * time.Second))
	laptop := hlc.New(hlc.WithWallClock(laptopWall))

	a := phone.Now()

	// The tablet syncs, sees a, and only then writes b.
	tablet.Observe(a)

	b := tablet.Now()
	c := laptop.Now()

	// The defect the correction names is exactly this edge: b causally follows
	// a, so its instant must not be earlier.
	if !b.After(a) {
		t.Fatalf("b was stamped %s, before the a it follows at %s", b, a)
	}

	// The other two edges are the tie-break itself, and both point at the
	// larger instant. With the bad edge gone the three cannot form a cycle:
	// a < b, and c is ordered against each of them by its own instant.
	if !b.After(c) {
		t.Fatalf("b at %s does not beat the concurrent c at %s, so the tie-break has not moved", b, c)
	}
}
