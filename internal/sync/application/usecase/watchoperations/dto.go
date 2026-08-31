package watchoperations

import "uuid"

// Input names the reader whose log is being watched.
type Input struct {
	// UserID is the reader. It is never optional: a position is scoped per
	// reader, so a head that crossed readers would mean nothing to either.
	UserID uuid.UUID
}

// Output is how far the log has got.
type Output struct {
	// LastPosition is this node's head position for the reader, and zero for a
	// log with nothing in it.
	//
	// It is the head and not a page boundary. Nothing is being delivered here,
	// so there is no page — the caller pulls from its own cursor until
	// PullOperations reports no more behind it.
	LastPosition int64
}
