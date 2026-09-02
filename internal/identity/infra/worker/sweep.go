// Package worker holds the identity slice's housekeeping: the sweep that
// removes credentials nothing can present any more.
//
// Every refresh writes a credential and consumes the one presented (D07), and
// every recovery writes one more, so identity.credentials grows by a row per
// device per access token lifetime for as long as the node runs. Nothing
// depends on the rows going: an expired credential is refused whether or not
// it is still there. What depends on it is the table not being the node's
// largest, and a sweep is the whole of what that costs.
//
// It is a worker for the reason the replication pass is one: nobody calls it,
// so nothing would start it, and the node is what starts the things that run
// on their own.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// interval is how often the sweep runs.
//
// A constant rather than a setting, because nothing an operator knows would
// change it: the rows removed are refused already, so sweeping more often
// buys nothing, and sweeping less often costs only rows. An hour keeps the
// table within an hour of the sessions that exist.
const interval = time.Hour

// Sweep removes expired credentials on a timer.
type Sweep struct {
	credentials credential.Repository
	clock       service.Clock
	logger      *slog.Logger
}

// New returns the worker over the repository it sweeps.
func New(credentials credential.Repository, clock service.Clock, logger *slog.Logger) *Sweep {
	return &Sweep{credentials: credentials, clock: clock, logger: logger}
}

// Run sweeps until ctx ends. It always returns nil: a sweep that failed is
// logged and tried again at the next tick, because a node that stopped over
// housekeeping would be a node that stopped over nothing.
func (s *Sweep) Run(ctx context.Context) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.InfoContext(ctx, "the credential sweep is running", slog.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			s.logger.InfoContext(ctx, "the credential sweep stopped")

			return nil
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// Once removes every credential that has expired by now, and reports how
// many. It is what a tick does, exported so that a test can do it without a
// timer.
func (s *Sweep) Once(ctx context.Context) (int64, error) {
	return s.credentials.DeleteExpired(ctx, s.clock.Now())
}

// sweep is one tick.
func (s *Sweep) sweep(ctx context.Context) {
	removed, err := s.Once(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "the credential sweep failed", logging.Err(err))

		return
	}

	level := slog.LevelDebug
	if removed > 0 {
		level = slog.LevelInfo
	}

	s.logger.LogAttrs(ctx, level, "the credential sweep finished", slog.Int64("removed", removed))
}
