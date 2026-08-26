package persist

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// opTransaction is the operation reported when the transaction itself fails,
// as opposed to the work running inside it.
const opTransaction = "shared/persist: transaction"

// defaultMaxAttempts bounds how often a transaction is replayed after losing a
// race. Two conflicting writers need one retry; needing more than a couple
// means contention a retry loop will not fix, and looping longer only converts
// a fast error into a slow one.
const defaultMaxAttempts = 3

// txKey addresses the transaction a context is running in.
type txKey struct{}

// Manager runs work inside a database transaction and carries that transaction
// through the context, so that repositories composed into one use case commit
// or roll back together.
//
// The zero value is not usable; build one with [NewManager].
type Manager struct {
	pool *pgxpool.Pool
}

// NewManager returns a manager over pool.
//
// Transactions run at the isolation level configured on the server, which is
// READ COMMITTED unless it was changed. The reconciler needs more than that
// and asks for it explicitly with [Manager.WithinOptions]; making every
// transaction in the node repeatable-read to serve that one caller would pay
// for it on every request.
func NewManager(pool *pgxpool.Pool) *Manager {
	return &Manager{pool: pool}
}

// Executor returns the transaction ctx is running in, or the pool when it is
// running outside one.
//
// This is what a repository calls. It never returns nil, so a repository
// written against it works unchanged whether or not a use case wrapped it in a
// transaction.
func (m *Manager) Executor(ctx context.Context) Executor {
	if tx, ok := txFrom(ctx); ok {
		return tx
	}

	return m.pool
}

// InTransaction reports whether ctx is already running inside a transaction.
func (m *Manager) InTransaction(ctx context.Context) bool {
	_, ok := txFrom(ctx)

	return ok
}

// Within runs fn inside a transaction, committing when it returns nil and
// rolling back when it returns an error or panics.
//
// When ctx already carries a transaction, fn joins it: the call does not nest,
// and the outermost Within is what decides the outcome. A use case can
// therefore call another without either having to know whether it is the one
// that opened the transaction. The consequence is that an inner failure aborts
// the whole unit of work — which is the point, and why an inner Within must
// not swallow its own error and report success.
//
// A transaction that lost a race — a serialization failure or a deadlock — is
// replayed from the start, up to [defaultMaxAttempts] times. fn is called
// again from scratch, so it must not depend on state it mutated outside the
// database on a previous attempt.
func (m *Manager) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.WithinOptions(ctx, pgx.TxOptions{}, fn)
}

// WithinOptions is [Manager.Within] with explicit transaction options, for the
// callers that need a stronger isolation level or a read-only transaction.
//
// The options are ignored when ctx already carries a transaction: the level of
// a transaction cannot be raised once it has read anything, and silently
// running under a weaker one is the kind of difference that shows up only
// under load. Open the outermost transaction with the level the innermost work
// requires.
//
// The options are taken by value, as the driver itself takes them: a pointer
// would let a caller mutate the options of a transaction already in flight.
//
//nolint:gocritic // hugeParam: see the paragraph above, this is deliberate.
func (m *Manager) WithinOptions(
	ctx context.Context,
	options pgx.TxOptions,
	fn func(ctx context.Context) error,
) error {
	if m.InTransaction(ctx) {
		return fn(ctx)
	}

	var err error

	for attempt := 1; attempt <= defaultMaxAttempts; attempt++ {
		err = m.runOnce(ctx, &options, fn)
		if !errors.Is(err, errs.KindConflict) {
			return err
		}

		// The context is checked before replaying, so that a caller who has
		// already given up does not pay for another attempt.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Classify(ctxErr, opTransaction)
		}

		logging.From(ctx).WarnContext(ctx, "retrying transaction after a concurrent write",
			slog.Int("attempt", attempt), slog.Int("max_attempts", defaultMaxAttempts), logging.Err(err))
	}

	return err
}

// runOnce is a single attempt at the transaction.
//
// The begin, commit and rollback are [pgx.BeginTxFunc]: it commits when fn
// returns nil, rolls back when it returns an error, and — because its cleanup
// is a plain defer that does not recover — rolls back on a panic as well,
// letting the panic continue to the recovery interceptor.
//
// One consequence is worth knowing. pgx sends the ROLLBACK on the caller's
// context, so a request canceled mid-transaction never gets the statement
// through; pgx then discards that pooled connection instead of returning it,
// and the server aborts the transaction when it notices the connection is
// gone. The outcome is correct, the cost is one connection.
func (m *Manager) runOnce(
	ctx context.Context,
	options *pgx.TxOptions,
	fn func(ctx context.Context) error,
) error {
	// fn's error is captured rather than read back from BeginTxFunc, which
	// cannot say whether what it returns came from the work or from the
	// transaction control around it. The distinction matters: the caller's
	// error is already in the vocabulary of this node and must reach them
	// untouched, while a failed BEGIN or COMMIT is a driver error nobody has
	// classified yet.
	var workErr error

	txErr := pgx.BeginTxFunc(ctx, m.pool, *options, func(tx pgx.Tx) error {
		workErr = fn(txInto(ctx, tx))

		return workErr
	})

	switch {
	case workErr != nil:
		return workErr
	case txErr != nil:
		return Classify(txErr, opTransaction)
	default:
		return nil
	}
}

// txInto returns a context running inside tx.
func txInto(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// txFrom returns the transaction ctx is running in, if any.
func txFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)

	return tx, ok
}
