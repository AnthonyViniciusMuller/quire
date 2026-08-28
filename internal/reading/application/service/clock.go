package service

import "time"

// Clock is where this slice reads the time.
//
// It is a port and not a call to time.Now for the reasons the library slice's
// is: a use case that stamps a record with the instant it was written has to be
// testable against a fixed instant, and the resolution is a decision rather
// than a detail — a Go instant carries nanoseconds and the timestamptz column
// carries microseconds, so a value written and read back differs from the one
// still in memory unless something rounds first.
//
// And, as there, what it stamps is not a wall clock at all. Every write here
// carries a causal timestamp that C01 requires to be monotonic rather than a
// reading of the local clock. The entities apply the per-record half of that
// rule; what satisfies this port applies the node-wide half, over
// internal/shared/hlc. Swapping the wall clock for it changed no use case,
// which is what the port was for.
type Clock interface {
	// Now is the current instant, in UTC, at the resolution the database keeps.
	Now() time.Time
}
