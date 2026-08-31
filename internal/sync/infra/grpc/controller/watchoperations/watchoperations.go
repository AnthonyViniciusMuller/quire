// Package watchoperations serves SyncService.WatchOperations: UC10 for a caller
// that cannot hold the bidirectional stream open.
//
// A browser is the caller it exists for. gRPC-Web carries a unary call and a
// server stream and neither of the other two, so Sync is unreachable from one
// (D10). What this restores is the half of UC10 that a server stream can carry:
// the node telling a device that something happened, rather than the device
// finding out at its next poll.
//
// # Why it notifies rather than delivers
//
// It sends a position and never an operation. A caller that hears one answers
// with PullOperations, which is the same call it would have made anyway and the
// same page it would have got — so the delivery path stays the one that already
// exists, and this stream adds no second way for an operation to arrive.
//
// That is what removes the acknowledgement. The Sync stream holds a page in
// flight and waits for SyncAck because an operation written to a socket is not
// an operation written to disk; nothing is in flight here, the cursor never
// leaves the caller, and a notification that was dropped costs the caller a
// poll rather than a change.
//
// # What wakes it
//
// The same two things that wake the Sync stream, minus the push: the hub, the
// moment another call on this node grows the reader's log, and a ticker,
// because the hub is in-process and two streams open against two replicas of
// this node do not wake each other. Neither is load-bearing, for the reason
// changes.Hub gives: a missed hint is a latency and not a loss, because the
// cursor is what the design rests on.
package watchoperations

import (
	"context"
	"time"
	"uuid"

	"google.golang.org/grpc/status"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	watchusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/watchoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/service/changes"
)

// WatchOperations serves the call.
type WatchOperations struct {
	head    command.Usecase[watchusecase.Input, watchusecase.Output]
	changes *changes.Hub
	poll    time.Duration
}

// New returns the controller over the use case, the hub that wakes it and the
// interval it falls back on.
func New(
	head command.Usecase[watchusecase.Input, watchusecase.Output],
	hub *changes.Hub,
	poll time.Duration,
) *WatchOperations {
	return &WatchOperations{head: head, changes: hub, poll: poll}
}

// Handle runs one stream until the caller closes it or the node stops.
func (w *WatchOperations) Handle(
	request *quirev1.WatchOperationsRequest, stream quirev1.SyncService_WatchOperationsServer,
) error {
	ctx := stream.Context()

	identity, err := authn.Require(ctx)
	if err != nil {
		return err
	}

	// Watching before the first report, and not after: a change committed
	// between the two would otherwise be one this stream never hears about and
	// the caller waits a whole poll for.
	woken, release := w.changes.Watch(identity.UserID)
	defer release()

	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	// The caller's cursor is where the reporting starts, so that a backlog is
	// announced at once rather than on the first change after connecting.
	reported := request.GetAfterPosition()

	if err := w.report(ctx, stream, identity.UserID, &reported); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-woken:
			if err := w.report(ctx, stream, identity.UserID, &reported); err != nil {
				return err
			}
		case <-ticker.C:
			if err := w.report(ctx, stream, identity.UserID, &reported); err != nil {
				return err
			}
		}
	}
}

// report sends the head position when it has moved past what this stream last
// said, and does nothing when it has not.
//
// The comparison is what makes the hint coalescing rather than chatty: the hub
// wakes a stream once per push, and a caller that has been told the log reaches
// position 90 learns nothing from being told again. It also makes the stream
// silent for a reader nobody is writing for, which is the common case and
// should cost nothing.
func (w *WatchOperations) report(
	ctx context.Context,
	stream quirev1.SyncService_WatchOperationsServer,
	reader uuid.UUID,
	reported *int64,
) error {
	output, err := w.head.Execute(ctx, watchusecase.Input{UserID: reader})
	if err != nil {
		return err
	}

	if output.LastPosition <= *reported {
		return nil
	}

	if err := stream.Send(&quirev1.WatchOperationsResponse{
		LastPosition: output.LastPosition,
	}); err != nil {
		return err
	}

	*reported = output.LastPosition

	return nil
}
