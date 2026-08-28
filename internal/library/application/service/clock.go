package service

import "time"

// Clock is where this slice reads the time.
//
// It is a port and not a call to time.Now for three reasons. A use case that
// stamps a record with the instant it was written has to be testable against a
// fixed instant, or its test is either loose or slow. The resolution is a
// decision rather than a detail: a Go instant carries nanoseconds and the
// timestamptz column carries microseconds, so a value written and read back
// differs from the one still in memory unless something rounds first.
//
// And, unlike the identity and federation slices, what this one stamps is not
// a wall clock at all. Every write here carries a crdt.Revision whose timestamp
// breaks the tie between concurrent versions, and C01 says that value has to be
// causally monotonic rather than a reading of the local clock. The entity
// applies the per-record half of that rule; what satisfies this port applies
// the node-wide half, over internal/shared/hlc. Swapping the wall clock for it
// changed no use case, which is what the port was for.
type Clock interface {
	// Now is the current instant, in UTC, at the resolution the database keeps.
	Now() time.Time
}
