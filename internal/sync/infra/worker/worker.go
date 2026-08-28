// Package worker runs the replication pass on a timer.
//
// It is the one part of the node that does work nobody asked for. Every other
// path through this repository begins with a call — a device pushing, a reader
// listing, a peer replicating — and this one begins with a tick, because
// replication is driven from the side that owes the data and the side that owes
// it has nobody to be prompted by.
//
// It holds no logic of its own. What a pass does is
// internal/sync/application/usecase/replicate, and this is a loop around it: a
// use case that can be run once by hand is one a test can drive without a
// clock, and a loop with no logic in it is one that cannot disagree with the
// use case about what a pass is.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/replicate"
)

// Replication runs the pass until the node stops.
type Replication struct {
	pass     command.Usecase[replicate.Input, replicate.Output]
	interval time.Duration
	logger   *slog.Logger
}

// New returns the worker over the pass and the interval between two of them.
func New(
	pass command.Usecase[replicate.Input, replicate.Output],
	interval time.Duration,
	logger *slog.Logger,
) *Replication {
	return &Replication{pass: pass, interval: interval, logger: logger}
}

// Run drains the queue on every tick and returns when ctx is cancelled.
//
// A pass that failed does not stop the worker. The queue is durable and the
// backoff is in it, so the next tick starts where this one left off — and a
// node that stopped replicating because one pass hit a closed connection would
// stop for good, silently, on the one path nobody is waiting for an answer
// from.
//
// It returns nil on cancellation rather than the context's error, because a
// node shutting down is not a node that failed: it is one of two servers in an
// errgroup, and returning an error would take the other down with it and report
// a fault the operator does not have.
func (r *Replication) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.logger.InfoContext(ctx, "the replication worker is running",
		slog.Duration("interval", r.interval))

	for {
		select {
		case <-ctx.Done():
			r.logger.InfoContext(ctx, "the replication worker stopped")

			return nil
		case <-ticker.C:
			r.run(ctx)
		}
	}
}

// run is one pass, with its outcome written down.
//
// A pass that offered nothing is logged at debug and one that did anything at
// info: a node whose peers are up to date ticks for ever, and an operator
// reading the log wants the ticks that moved something.
func (r *Replication) run(ctx context.Context) {
	output, err := r.pass.Execute(ctx, replicate.Input{})
	if err != nil {
		r.logger.ErrorContext(ctx, "a replication pass failed", logging.Err(err))

		return
	}

	attributes := []slog.Attr{
		slog.Int64("enqueued", output.Enqueued),
		slog.Int("servers", output.Servers),
		slog.Int("offered", output.Offered),
		slog.Int64("confirmed", output.Confirmed),
		slog.Int64("failed", output.Failed),
	}

	level := slog.LevelInfo
	if output.Offered == 0 && output.Enqueued == 0 {
		level = slog.LevelDebug
	}

	r.logger.LogAttrs(ctx, level, "a replication pass finished", attributes...)
}
