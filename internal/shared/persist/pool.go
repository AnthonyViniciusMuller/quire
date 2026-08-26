package persist

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opOpen = "shared/persist: open"
	opPing = "shared/persist: ping"
)

// lifetimeJitterFraction spreads connection retirement over a tenth of the
// configured lifetime. Without it every connection opened during a rollout
// retires in the same instant, and the pool empties at once under load.
const lifetimeJitterFraction = 10

// Open opens the pool described by cfg and verifies that the database answers.
//
// The pool itself connects lazily, so a node would otherwise start reporting
// itself ready and only discover an unreachable database on the first request.
// One round trip at startup turns that into a failure at the point where the
// orchestrator can still act on it.
//
// The caller owns the returned pool and must Close it.
func Open(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	poolConfig, err := NewPoolConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the database pool could not be created").WithOp(opOpen)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()

		return nil, Classify(err, opPing)
	}

	return pool, nil
}

// NewPoolConfig translates the database section of the configuration into a
// driver configuration.
//
// It is exported so that a slice container can attach what only it knows —
// a type registration, a tracer — before opening the pool, and so that the
// translation can be asserted without a database.
func NewPoolConfig(cfg config.Database) (*pgxpool.Config, error) {
	// The connection string carries the password. The driver redacts it from
	// the parse error, which is what makes wrapping the cause safe here.
	poolConfig, err := pgxpool.ParseConfig(cfg.URL.Reveal())
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the database URL is malformed").WithOp(opOpen)
	}

	poolConfig.MaxConns = int32(cfg.MaxConnections)
	poolConfig.MinConns = int32(cfg.MinConnections)
	poolConfig.MaxConnLifetime = cfg.MaxConnectionLifetime
	poolConfig.MaxConnLifetimeJitter = cfg.MaxConnectionLifetime / lifetimeJitterFraction
	poolConfig.MaxConnIdleTime = cfg.MaxConnectionIdleTime
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	// Keeping the warm connections idle rather than merely open is what
	// removes the connection handshake from the latency of the first request
	// after a quiet period, which RNF06 budgets for.
	poolConfig.MinIdleConns = int32(cfg.MinConnections)

	return poolConfig, nil
}
