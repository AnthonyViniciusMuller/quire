// Package persist holds the PostgreSQL plumbing every slice shares: the
// connection pool, the unit of work that carries a transaction through the
// context, and the translation of driver errors into the vocabulary of
// [github.com/anthonyvsmuller/quire/internal/shared/errs].
//
// Repositories in internal/<slice>/infra/persist depend on [Executor] rather
// than on a pool or a transaction. A use case that has to write to two
// repositories atomically — registering a user and its first device, applying
// a batch of sync operations and advancing the stream position — wraps them in
// [Manager.Within] and neither repository has to know it happened.
//
// The transaction travels in the context and nowhere else. Passing it as an
// argument would put a database type in every application-layer signature,
// which is exactly the coupling the hexagonal layout exists to prevent; the
// alternative, a repository that opens its own transaction, cannot compose two
// repositories into one atomic step at all.
//
// No driver error escapes this package unclassified. A unique violation
// reaching a gRPC handler as an opaque *pgconn.PgError would be answered with
// Internal instead of AlreadyExists, and its text would name the table and the
// constraint to whoever asked.
package persist

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Executor is the query surface a repository needs. Both a pool and a
// transaction satisfy it, which is what lets one repository run inside or
// outside a transaction without knowing which.
//
// The method set is deliberately the one sqlc generates as DBTX for the pgx/v5
// driver, so that a generated Queries value can be built straight from an
// Executor.
type Executor interface {
	// Exec runs a statement and reports what it affected.
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	// Query runs a statement returning rows.
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	// QueryRow runs a statement returning at most one row. The error surfaces
	// on Scan.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
