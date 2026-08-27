package service

import "time"

// Clock is where this slice reads the time.
//
// It is a port and not a call to time.Now for two reasons. A use case that
// stamps a record with the instant it was written has to be testable against a
// fixed instant, or its test is either loose or slow. And the resolution is a
// decision rather than a detail: a Go instant carries nanoseconds and the
// timestamptz column carries microseconds, so a value written and read back
// differs from the one still in memory unless something rounds first. The
// implementation rounds, and this is where a reader is told so.
//
// It is a wall clock, and only for the columns that are wall clocks. What
// orders a replicated write is the hybrid logical clock of C01, which is a
// different thing entirely and belongs to the sync slice.
type Clock interface {
	// Now is the current instant, in UTC, at the resolution the database keeps.
	Now() time.Time
}
