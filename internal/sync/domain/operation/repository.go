package operation

import (
	"context"
	"uuid"
)

// The bounds a page of the log is read within.
//
// They are larger than the library slice's, and the reason is what a page is
// for: a page of works is something a reader looks at, and a page of
// operations is a device draining a backlog it accumulated while it was
// disconnected. The number that matters there is the round trips, not the
// screenful.
const (
	// DefaultPageSize is what a caller that asks for no particular size gets.
	DefaultPageSize = 500
	// MaxPageSize is the largest page the node will assemble, whatever was
	// asked for.
	MaxPageSize = 2000
)

// Query is one page of one reader's log.
type Query struct {
	// UserID is the reader whose log is being read. It is never optional: the
	// position is scoped per reader, so a page that crossed readers would have
	// no cursor at all.
	UserID uuid.UUID

	// AfterPosition is where to continue from, zero for the beginning of the
	// log — which is what a device that has just been bound asks for.
	AfterPosition int64

	// Size is how many operations to return, already clamped by the use case.
	Size int
}

// Repository is the port through which the use cases of the sync slice read
// and write the log. It belongs to the domain; what satisfies it lives in
// internal/sync/infra/repository/operation.
//
// As in every other slice, the context is passed so that a call can join the
// transaction the manager carries — and here that is not a convenience but the
// whole of C08: the position is allocated from a row lock held until commit,
// so an allocation that ran in a different transaction from its insert would
// release the lock before the operation was visible and the cursor would lose
// its guarantee.
type Repository interface {
	// Append stores an operation and stamps it with the position this node
	// allocated, reporting false when this node already had it.
	//
	// A false is not an error. An operation reaching a node twice by two
	// routes is the normal shape of a federation, and it is what the contract
	// answers with OPERATION_OUTCOME_DUPLICATE.
	Append(ctx context.Context, op *Operation) (bool, error)

	// List reads one page of a reader's log in ascending position order, and
	// reports whether more remain behind it.
	//
	// A caller that has seen position N has necessarily seen every position
	// below it, which is what allocating the number inside the writing
	// transaction buys and what lets the cursor be a single number rather than
	// a set.
	List(ctx context.Context, query *Query) ([]*Operation, bool, error)

	// ListByID reads operations by the identifiers their authors minted, in
	// reader and position order.
	//
	// It is what the replication worker loads a batch with: the delivery queue
	// names operations and not payloads, because the delta is stored once
	// however many peers it is owed to (C07).
	ListByID(ctx context.Context, ids []uuid.UUID) ([]*Operation, error)

	// Head is this node's last allocated position for a reader, and zero for a
	// reader whose log is empty.
	//
	// A device that has just pushed learns from it whether there is anything
	// to pull, without asking.
	Head(ctx context.Context, userID uuid.UUID) (int64, error)
}
