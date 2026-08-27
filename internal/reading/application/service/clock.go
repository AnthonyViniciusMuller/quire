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
// And, as there, what it stamps is not only a wall clock. Every write here
// carries a causal timestamp that C01 requires to be monotonic rather than a
// reading of the local clock. What the entities apply today is the per-record
// half of that rule; the node-wide hybrid logical clock of phase 9 is the other
// half, and it arrives as a different adapter behind this same port. No use
// case changes when it does.
type Clock interface {
	// Now is the current instant, in UTC, at the resolution the database keeps.
	Now() time.Time
}
