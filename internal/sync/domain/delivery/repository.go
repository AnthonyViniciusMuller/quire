package delivery

import (
	"context"
	"time"
	"uuid"
)

// The bounds a batch of pending deliveries is drained within.
//
// They are the log's own, because a batch of deliveries becomes a batch of
// operations on the wire: draining more of the queue than one replication call
// can carry would only make the worker hold rows it is not about to send.
const (
	// DefaultBatchSize is what a worker that asks for no particular size gets.
	DefaultBatchSize = 500
	// MaxBatchSize is the largest batch the node will assemble.
	MaxBatchSize = 2000
)

// MaxBackoffExponent bounds how far the retry interval doubles.
//
// Six doublings over a thirty-second base is about half an hour, which is the
// longest a peer that came back should wait to be noticed. Beyond that the
// interval stops growing: an unreachable node costs one call every half hour,
// and a node that has been down for a week is caught up in the same half hour
// as one that was down for an afternoon.
const MaxBackoffExponent = 6

// Batch is the pending deliveries owed to one node.
type Batch struct {
	// ServerID is the node being drained.
	ServerID uuid.UUID

	// Now is the instant the backoff is measured against, taken from the
	// slice's clock so that a test can drive the queue without waiting.
	Now time.Time

	// Backoff is the base interval a failed delivery waits before it is tried
	// again, doubled once per attempt up to [MaxBackoffExponent].
	Backoff time.Duration

	// Size is how many deliveries to take, already clamped by the caller.
	Size int
}

// Repository is the port through which the sync slice reads and writes what it
// owes its peers. It belongs to the domain; what satisfies it lives in
// internal/sync/infra/repository/delivery.
type Repository interface {
	// EnqueuePending owes every peer everything the readers who authorized it
	// have not offered it yet, and reports how many rows it wrote.
	//
	// It is what fills the queue, and it is a statement over the log rather
	// than a call the ingest makes for a reason that only shows up on the
	// second peer: a node authorized as a replica today holds none of the
	// reader's history (RF16, UC15), and rows written when a change happened
	// would carry only what happened afterwards. That peer would be
	// permanently missing everything from before its own authorization, and
	// nothing would ever notice. Filling from the log makes the two cases one
	// — a peer authorized a moment ago and a peer that missed a week are both
	// owed what they have not been offered.
	EnqueuePending(ctx context.Context) (int64, error)

	// Enqueue records that an operation is owed to each of the nodes, and does
	// nothing for a pair that already has a row.
	//
	// It must run inside the transaction that appended the operation. A change
	// committed without its delivery rows is a change no worker will ever
	// send, and nothing afterwards would notice: the queue is the only record
	// that the peer is owed anything.
	Enqueue(ctx context.Context, operationID uuid.UUID, servers []uuid.UUID) error

	// ListPending reads what is still owed to one node, oldest attempt first
	// and never-attempted rows before all of them, skipping the ones whose
	// backoff has not elapsed.
	ListPending(ctx context.Context, batch *Batch) ([]*Delivery, error)

	// PendingServers reads the nodes this instance owes anything to at all.
	//
	// The worker asks this rather than walking the catalogue, because the two
	// are different sets: a node authorized a moment ago is owed nothing yet,
	// and a node whose authorization was revoked may still be owed what was
	// enqueued before the revocation.
	PendingServers(ctx context.Context) ([]uuid.UUID, error)

	// Record applies the outcome of one try to every delivery of the batch,
	// and reports how many rows it changed.
	//
	// It takes the operations rather than the deliveries because that is what
	// the destination answers with: the reply carries one result per operation
	// offered, and the row it settles is the one for that operation and that
	// node. A row already confirmed is left alone, so a reply arriving twice
	// does not count a second attempt.
	Record(ctx context.Context, serverID uuid.UUID, operations []uuid.UUID, attempt *Attempt) (int64, error)
}
