// Package hlc is the node-wide hybrid logical clock every replicated timestamp
// on this node is stamped from.
//
// It is the other half of C01 in docs/tcc-corrections.md. The correction shows
// that `atualizado_em` cannot break a tie while it is a wall clock: ordinary
// skew between two devices, with no clock ever running backwards, is enough to
// make a causally later write carry an earlier instant, and one such edge turns
// the last-writer-wins relation into a cycle. A cycle has no maximum, merge
// stops being associative, and two nodes that saw the same writes in different
// orders converge on different values — which is exactly what RNF03 promises
// cannot happen.
//
// The rule the correction states is that every write stamps
//
//	t = max(local wall clock, greatest t this replica has observed + one step)
//
// and this is that rule over everything the node has seen. The half that
// applies it over a single record already exists, in
// [github.com/anthonyvsmuller/quire/internal/shared/crdt], where a revision is
// stamped no earlier than one step past the version it was derived from. The
// two compose, because a maximum of maxima is a maximum: this clock
// strengthens the per-record floor without changing it, and the floor is what
// still holds when this one is restarted.
//
// # What a restart loses, and why it is not the guarantee
//
// The greatest observed instant is held in memory. A node that restarts
// forgets it and begins again from its wall clock, which on a node whose clock
// runs behind is below what it had already stamped.
//
// That is survivable, and the reason is where the cycle of C01 lives. Merge is
// per record: the three writes in the counterexample are three versions of one
// row, and the edge that has to point forwards is the edge between a version
// and the version it was derived from. Every write on this node reads the
// record it is about to stamp — a use case that updates a work reads the work,
// and the reconciler reads the record before it decides — so the per-record
// floor applies to all of them, restart or not. What this clock adds on top is
// that instants stamped on unrelated records are ordered too, which makes the
// node's whole history readable in one order rather than five.
//
// # Why not a library
//
// Every hybrid logical clock published for Go implements the shape from
// Kulkarni et al.: a pair of a physical instant and a logical counter, which
// is two values. github.com/lafikl/hlc is the most used of them at 44 stars,
// last committed in 2017, never tagged, and its Timestamp holds `ts` and
// `count` as unexported fields with no accessor — a value that cannot be
// written to a column or read back from one. CockroachDB's util/hlc is the
// only production implementation, and it is a package inside the module that
// is the whole database; its Timestamp is a protobuf message with WallTime,
// Logical and Synthetic.
//
// The pair is what Quire cannot take. C01 stamps a single `timestamptz`,
// because the value has to live in the `atualizado_em` column that already
// exists on five tables and has to compare as an instant wherever it travels;
// a second column for the counter is precisely the schema change C02 settled
// against. What the microsecond resolution of that column buys is that the
// "+1" of the rule has somewhere to go without a counter beside it — one step
// is the smallest difference the column can still represent. The algorithm
// that remains is the maximum below, and a dependency that supplied the wrong
// data type would not have saved it.
package hlc

import (
	"sync"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// MaxDrift is how far ahead of this node's wall clock an observed instant may
// be and still be adopted.
//
// Without a ceiling, one peer whose clock is a year fast would push this
// node's clock a year into the future and keep it there: every instant it
// stamped afterwards would be ahead of real time, and every tie against a
// correctly stamped write would go to whichever record had been poisoned. The
// ceiling bounds that to the ceiling itself.
//
// Refusing the observation is safe for the same reason a restart is. The
// causal edge that must point forwards is the one between two versions of a
// record, and the per-record floor holds it regardless of what this clock
// adopted; what is given up is only that unrelated records stay in one order
// with a node that is a long way off.
//
// It is a constant and not a setting. Five minutes is a margin for skew
// between machines that are trying to keep time, not a knob for how much
// disagreement a federation tolerates, and an operator handed the second would
// eventually set it to a year.
const MaxDrift = 5 * time.Minute

// Clock is the node-wide hybrid logical clock.
//
// It is safe for concurrent use, which the ports it satisfies require: one
// clock serves every slice of the node, and the whole point of it is that two
// writes racing on two connections cannot be stamped with the same instant.
//
// The zero value is not usable; build one with [New].
type Clock struct {
	// wall is where the physical reading comes from. It is a field rather than
	// a call to time.Now so that a test can drive the clock backwards, which
	// is the condition the whole type exists for and one a real machine offers
	// on nobody's schedule.
	wall func() time.Time

	mu sync.Mutex
	// observed is the greatest instant this replica has stamped or seen.
	observed time.Time
}

// Option configures a clock.
type Option func(*Clock)

// WithWallClock replaces the physical reading, for tests.
func WithWallClock(wall func() time.Time) Option {
	return func(c *Clock) {
		if wall != nil {
			c.wall = wall
		}
	}
}

// New returns a clock reading the machine's wall clock.
//
// It starts having observed nothing. The first instant it returns is therefore
// the wall clock reading, which is what a node that has just started should
// stamp — and what the per-record floor corrects when the record it is
// stamping already carries something later.
func New(options ...Option) *Clock {
	clock := &Clock{wall: time.Now}

	for _, option := range options {
		option(clock)
	}

	return clock
}

// Now stamps a write made on this node.
//
// The instant returned is strictly greater than every instant this clock has
// returned or observed, so two writes on this node never carry the same one
// and a write made after seeing a peer's is stamped after it. That is C01's
// rule with the maximum taken over the whole node.
//
// It is at the resolution the column keeps, and in UTC, because the value
// travels: it is written to a timestamptz, rendered onto the wire as a hybrid
// timestamp, and compared against instants stamped on other nodes in other
// zones.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	stamped := c.wall().UTC().Truncate(crdt.Resolution)

	if floor := c.observed.Add(crdt.Resolution); stamped.Before(floor) {
		stamped = floor
	}

	c.observed = stamped

	return stamped
}

// Observe folds an instant this replica has seen into the clock, and reports
// whether it was adopted.
//
// It is what the reconciler calls for every operation it ingests, and it is
// what makes the rule hold across the federation rather than only within this
// process: a local write that follows a remote one must be stamped after it,
// and the only way this node can know what "after" is is to have been told.
//
// A false is not an error and not a rejection of the operation. It says the
// instant is further ahead of this node's wall clock than [MaxDrift], so the
// clock did not follow it there; the operation is stored and reconciled
// exactly as any other, and what the caller does with the answer is report it,
// because a peer that far off is a fault an operator has to see.
func (c *Clock) Observe(at time.Time) bool {
	if at.IsZero() {
		return false
	}

	seen := at.UTC().Truncate(crdt.Resolution)

	c.mu.Lock()
	defer c.mu.Unlock()

	if seen.After(c.wall().UTC().Add(MaxDrift)) {
		return false
	}

	if seen.After(c.observed) {
		c.observed = seen
	}

	return true
}

// Observed is the greatest instant this clock has stamped or adopted.
//
// It is a reading and not a stamp: it advances nothing, and two calls with no
// write between them return the same value. What it is for is the operator —
// a node whose observed instant is far ahead of its wall clock is a node that
// met a peer with a broken one.
func (c *Clock) Observed() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.observed
}
