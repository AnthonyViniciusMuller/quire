package pulloperations

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Input is a request for everything after a cursor.
type Input struct {
	// UserID is the reader whose log is being read.
	UserID uuid.UUID

	// AfterPosition is the cursor, and zero asks for the whole log — which is
	// what a device that has just been bound asks for.
	//
	// It is this node's position. A device that also pulls from a replica
	// keeps that node's cursor separately, and the two numbers have nothing to
	// do with each other.
	AfterPosition int64

	// Limit is how many changes to return, and zero asks the node to choose.
	Limit int
}

// Output is one page of the log.
type Output struct {
	// Operations are the changes, in ascending position order.
	Operations []*operation.Operation

	// LastPosition is the position of the last change returned, and the cursor
	// to send next time. It is not this node's head: with HasMore true there is
	// more behind it, and on an empty page it is the cursor the caller sent —
	// a device that asked and was told nothing must not rewind.
	LastPosition int64

	// HasMore reports whether there is another page behind this one.
	HasMore bool
}
