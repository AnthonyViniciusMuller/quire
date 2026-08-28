package service

import "time"

// Clock is where this slice reads the time, and where it tells the node what
// time it has been told about.
//
// It is the only Clock port in the node with a second method, and the reason is
// that this is the slice that meets other people's clocks. C01 requires every
// replicated instant to be causally monotonic: a local write must be stamped
// after everything this replica has observed, and an operation arriving from a
// device or a peer is exactly such an observation. A port with only Now would
// leave the node stamping from a clock that had never heard of the federation
// it is in.
type Clock interface {
	// Now is the instant to stamp a write made here with, in UTC and at the
	// resolution the database keeps.
	Now() time.Time

	// Observe folds an instant this node has been told about into the clock,
	// and reports whether it was adopted.
	//
	// A false is not a rejection of the operation that carried it. It says the
	// instant is further ahead of this node's wall clock than the node is
	// willing to follow, which is a fault an operator has to see and not
	// something the reader can do anything about.
	Observe(at time.Time) bool
}
