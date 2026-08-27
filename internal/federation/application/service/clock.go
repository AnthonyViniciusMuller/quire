package service

import "time"

// Clock is where this slice reads the time.
//
// It is a port and not a call to time.Now for two reasons. A use case that
// stamps a record with the instant it was written — when a node was
// discovered, when a reader authorized one — has to be testable against a
// fixed instant, or its test is either loose or slow. And the resolution is a
// decision rather than a detail: a Go instant carries nanoseconds and the
// timestamptz column carries microseconds, so a value written and read back
// differs from the one still in memory unless something rounds first.
//
// It is a wall clock, and only for the columns that are wall clocks. Nothing
// in this slice is reconciled, so nothing here reads the hybrid logical clock
// of C01.
type Clock interface {
	// Now is the current instant, in UTC, at the resolution the database keeps.
	Now() time.Time
}
