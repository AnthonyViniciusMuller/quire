// Package apptest holds the doubles the sync slice's use case tests are
// written against.
//
// One package for the slice rather than a double redefined per test file: the
// use cases here depend on the same handful of ports, and a fake written five
// times drifts five ways. It is imported only by tests.
//
// They are fakes and not mocks, and in this slice that distinction earns more
// than it does elsewhere. The log enforces what the schema enforces —
// deduplication by the identifier the author minted, and a position allocated
// in commit order — so a test can exercise the duplicate that a unique index
// decides in production, and can assert that a cursor never skips.
package apptest

import (
	"context"
	"slices"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Clock is a clock stopped at a fixed instant, which also records everything
// it has been told about.
//
// It steps by one resolution unit on every reading, as the node's own does, so
// that two writes in one test cannot be stamped with the same instant and a
// test cannot pass because of a tie the real clock never produces.
type Clock struct {
	mu       sync.Mutex
	instant  time.Time
	observed []time.Time
	// Refuse, when set, is what Observe reports, standing for an instant
	// further ahead than the node will follow.
	Refuse bool
}

// Clock satisfies the port the use cases hold.
var _ service.Clock = (*Clock)(nil)

// NewClock returns a clock that first reads instant.
func NewClock(instant time.Time) *Clock {
	return &Clock{instant: instant.UTC().Truncate(crdt.Resolution)}
}

// Now returns the current instant and steps past it.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	reading := c.instant
	c.instant = c.instant.Add(crdt.Resolution)

	return reading
}

// Observe records the instant and reports whether it was adopted.
func (c *Clock) Observe(at time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.observed = append(c.observed, at)

	return !c.Refuse
}

// Observed is every instant the clock has been told about, in order.
func (c *Clock) Observed() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.observed)
}

// Transaction runs the work directly and undoes the log on a failure.
//
// There is no database behind these doubles, so the rollback is simulated
// rather than real — but simulating it is the point: the use case unwinds the
// unit of work of a change the reconciler refused, and a fake that committed
// anyway would let a test pass while the log kept an operation the node
// answered "rejected" to.
type Transaction struct {
	log *OperationRepository

	mu    sync.Mutex
	calls int
	// Err, when set, is what Within reports without running the work.
	Err error
}

// Transaction satisfies the port the use cases hold.
var _ service.Transaction = (*Transaction)(nil)

// NewTransaction returns a unit of work over the log it can undo.
func NewTransaction(log *OperationRepository) *Transaction {
	return &Transaction{log: log}
}

// Within runs fn, restoring the log when it fails.
func (t *Transaction) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	t.mu.Lock()
	t.calls++
	err := t.Err
	t.mu.Unlock()

	if err != nil {
		return err
	}

	undo := t.log.snapshot()

	if err = fn(ctx); err != nil {
		t.log.restore(undo)

		return err
	}

	return nil
}

// Calls is how often a unit of work was opened.
func (t *Transaction) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.calls
}

// Records is a reconciler that answers with whatever the test set, and records
// what it was asked about.
type Records struct {
	mu       sync.Mutex
	verdicts map[uuid.UUID]operation.Verdict
	seen     []uuid.UUID
	// Err, when set, is what Reconcile reports — the node failing rather than
	// a verdict.
	Err error
}

// Records satisfies the port the use cases hold.
var _ service.Records = (*Records)(nil)

// NewRecords returns a reconciler that applies everything it is given.
func NewRecords() *Records {
	return &Records{verdicts: map[uuid.UUID]operation.Verdict{}}
}

// Answer fixes the verdict on one operation.
func (r *Records) Answer(id uuid.UUID, verdict operation.Verdict) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.verdicts[id] = verdict
}

// Reconcile answers, defaulting to applied.
func (r *Records) Reconcile(
	_ context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Err != nil {
		return operation.Verdict{}, r.Err
	}

	r.seen = append(r.seen, op.ID)

	if verdict, fixed := r.verdicts[op.ID]; fixed {
		return verdict, nil
	}

	return operation.Applied(), nil
}

// Seen is every operation the reconciler was asked about, in order.
func (r *Records) Seen() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.seen)
}

// OperationRepository is an in-memory log that numbers the way the statement
// does: one counter per reader, allocated on the way in, and an operation
// already present consuming a number and then doing nothing.
type OperationRepository struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]*operation.Operation
	byUser map[uuid.UUID][]*operation.Operation
	heads  map[uuid.UUID]int64
	// Err, when set, is what every method reports.
	Err error
}

// OperationRepository satisfies the port the use cases hold.
var _ operation.Repository = (*OperationRepository)(nil)

// NewOperationRepository returns an empty log.
func NewOperationRepository() *OperationRepository {
	return &OperationRepository{
		byID:   map[uuid.UUID]*operation.Operation{},
		byUser: map[uuid.UUID][]*operation.Operation{},
		heads:  map[uuid.UUID]int64{},
	}
}

// Append stores an operation, reporting false when the log already had it.
func (r *OperationRepository) Append(_ context.Context, op *operation.Operation) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Err != nil {
		return false, r.Err
	}

	// The position is consumed whether or not the operation is stored, which
	// is what the statement does and what makes a gap in the log ordinary.
	r.heads[op.UserID]++

	if _, held := r.byID[op.ID]; held {
		return false, nil
	}

	op.PlaceAt(r.heads[op.UserID])
	stored := *op
	r.byID[op.ID] = &stored
	r.byUser[op.UserID] = append(r.byUser[op.UserID], &stored)

	return true, nil
}

// List reads one page of a reader's log and reports whether more remain.
func (r *OperationRepository) List(
	_ context.Context, query *operation.Query,
) ([]*operation.Operation, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Err != nil {
		return nil, false, r.Err
	}

	page := make([]*operation.Operation, 0, query.Size)

	for _, op := range r.byUser[query.UserID] {
		if op.Position > query.AfterPosition {
			page = append(page, op)
		}
	}

	slices.SortFunc(page, func(left, right *operation.Operation) int {
		return int(left.Position - right.Position)
	})

	if len(page) > query.Size {
		return page[:query.Size], true, nil
	}

	return page, false, nil
}

// ListByID reads operations by identifier, in reader and position order.
func (r *OperationRepository) ListByID(
	_ context.Context, ids []uuid.UUID,
) ([]*operation.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Err != nil {
		return nil, r.Err
	}

	found := make([]*operation.Operation, 0, len(ids))

	for _, id := range ids {
		if op, held := r.byID[id]; held {
			found = append(found, op)
		}
	}

	slices.SortFunc(found, func(left, right *operation.Operation) int {
		if left.UserID != right.UserID {
			return left.UserID.Compare(right.UserID)
		}

		return int(left.Position - right.Position)
	})

	return found, nil
}

// Head is the last position allocated for a reader.
func (r *OperationRepository) Head(_ context.Context, userID uuid.UUID) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Err != nil {
		return 0, r.Err
	}

	return r.heads[userID], nil
}

// Holds reports whether the log has the operation.
func (r *OperationRepository) Holds(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, held := r.byID[id]

	return held
}

// Len is how many operations the log holds.
func (r *OperationRepository) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.byID)
}

// snapshot is what a unit of work restores the log to when it fails.
func (r *OperationRepository) snapshot() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()

	held := make([]uuid.UUID, 0, len(r.byID))
	for id := range r.byID {
		held = append(held, id)
	}

	return held
}

// restore removes everything written since the snapshot. The positions it
// consumed are left consumed, as a rolled-back transaction leaves them.
func (r *OperationRepository) restore(held []uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id := range r.byID {
		if slices.Contains(held, id) {
			continue
		}

		user := r.byID[id].UserID
		delete(r.byID, id)

		r.byUser[user] = slices.DeleteFunc(r.byUser[user], func(op *operation.Operation) bool {
			return op.ID == id
		})
	}
}

// Changes records what was announced, so that a test can assert that a stream
// would have been woken — and, more usefully, that it would not have been for a
// batch that grew nothing.
type Changes struct {
	mu        sync.Mutex
	announced []uuid.UUID
}

// Changes satisfies the port the use cases hold.
var _ service.Changes = (*Changes)(nil)

// NewChanges returns a hub that records.
func NewChanges() *Changes { return &Changes{} }

// Announce records the reader.
func (c *Changes) Announce(userID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.announced = append(c.announced, userID)
}

// Announced is every reader announced, in order.
func (c *Changes) Announced() []uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.announced)
}
