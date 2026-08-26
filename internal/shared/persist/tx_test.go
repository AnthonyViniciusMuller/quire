package persist

// The transaction travels through the context under an unexported key, and
// that is the point: nothing outside this package can put one there, or take
// one out. Testing it therefore has to happen from inside the package. What a
// caller can observe — that a repository gets a working [Executor] either way —
// is covered here too, and again against a real database by the integration
// suites.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubTx stands in for a transaction. Only its identity matters here, so the
// embedded interface is left nil: any method call is a bug in the test.
type stubTx struct{ pgx.Tx }

// testPool builds a pool that never connects. pgxpool dials lazily, and no
// test in this file issues a query.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), "postgres://quire@localhost:5432/quire?sslmode=disable")
	if err != nil {
		t.Fatalf("building the pool returned %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

func TestExecutorFallsBackToThePoolOutsideATransaction(t *testing.T) {
	t.Parallel()

	manager := NewManager(testPool(t))
	ctx := t.Context()

	// A repository written against Executor has to work whether or not a use
	// case wrapped it in a transaction, which is what makes it composable.
	if manager.Executor(ctx) == nil {
		t.Fatal("Executor returned nil outside a transaction")
	}

	if manager.InTransaction(ctx) {
		t.Error("InTransaction is true outside a transaction")
	}
}

func TestExecutorReturnsTheTransactionCarriedByTheContext(t *testing.T) {
	t.Parallel()

	manager := NewManager(testPool(t))
	tx := &stubTx{}
	ctx := txInto(t.Context(), tx)

	if got := manager.Executor(ctx); got != Executor(tx) {
		t.Errorf("Executor returned %#v, want the transaction in the context", got)
	}

	if !manager.InTransaction(ctx) {
		t.Error("InTransaction is false inside a transaction")
	}
}

func TestWithinJoinsTheTransactionAlreadyInFlight(t *testing.T) {
	t.Parallel()

	manager := NewManager(testPool(t))
	tx := &stubTx{}
	outer := txInto(t.Context(), tx)

	called := false

	// Nesting must not open a second transaction: two use cases composed into
	// one must commit or roll back together, and the pool here would fail the
	// moment anything tried to dial.
	err := manager.Within(outer, func(inner context.Context) error {
		called = true

		if got := manager.Executor(inner); got != Executor(tx) {
			t.Errorf("the inner context runs on %#v, want the transaction already in flight", got)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Within returned %v", err)
	}

	if !called {
		t.Error("Within did not call the function")
	}
}

func TestWithinReportsTheFailureOfAJoinedCall(t *testing.T) {
	t.Parallel()

	manager := NewManager(testPool(t))
	ctx := txInto(t.Context(), &stubTx{})
	want := errors.New("the use case failed")

	// An inner failure has to abort the whole unit of work. Swallowing it here
	// would let the outermost Within commit a half-applied change.
	if err := manager.Within(ctx, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Errorf("Within returned %v, want %v", err, want)
	}
}

func TestTxFromIgnoresAnUnrelatedContext(t *testing.T) {
	t.Parallel()

	if _, ok := txFrom(t.Context()); ok {
		t.Error("txFrom found a transaction in a context that carries none")
	}
}
