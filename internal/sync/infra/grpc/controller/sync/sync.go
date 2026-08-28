// Package sync serves SyncService.Sync: UC10 and UC11 in one call.
//
// It is the same push and pull the two unary methods serve, kept open, so that
// a change made on one device reaches the reader's other devices as it happens
// rather than at the next poll, and so that a device which has just reconnected
// drains its backlog through the stream it then leaves open.
//
// # What wakes it
//
// Three things, and the order is the order of their reliability. The hub wakes
// it the moment another call on this node grows the reader's log, which is what
// makes the stream a stream. A poll wakes it periodically, because the hub is
// in-process and two devices whose streams are open against two replicas of
// this node do not wake each other. And a push on this stream wakes it
// directly, since a device that has just written wants to know what else it
// missed.
//
// None of the three is load-bearing. A stream that missed every one of them
// still leaves the device with a cursor, and the next pull carries the change
// — which is the property C08 exists to give and the reason a missed hint is a
// latency and not a loss.
//
// # What the acknowledgement is for
//
// The node keeps at most one page in flight. It sends, and it does not send
// again until the device has confirmed what it already has, because an
// operation written to a socket is not an operation written to disk and a
// device that dies in between must come back to the same place. That is the
// function SyncAck has in this contract, and the consequence is worth stating:
// a client that never acknowledges receives one page and then only what its own
// pushes provoke. The contract asks a device to acknowledge, and this is what
// happens when it does not.
package sync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	pullusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pulloperations"
	pushusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/service/changes"
)

// opHandle is the operation reported by this file, in the form the errs package
// expects.
const opHandle = "sync/stream: handle"

// The stable machine-readable codes this controller raises.
const (
	// CodeNoStart is a stream whose first message was not the start.
	CodeNoStart = "sync_stream_not_started"
	// CodeRestarted is a stream that sent a second start.
	CodeRestarted = "sync_stream_restarted"
	// CodeEmptyRequest is a message carrying none of the three payloads.
	CodeEmptyRequest = "sync_stream_empty_request"
)

// maxInFlight is how many positions the node will send ahead of the device's
// confirmation.
//
// It is one page, which is a bound on positions rather than on operations: a
// position is consumed by an operation this node already had, so the window is
// conservative — it never lets more than a page through and sometimes lets
// less. That is the right direction for a bound whose purpose is to stop a
// device being handed a backlog it cannot store.
const maxInFlight = operation.DefaultPageSize

// Sync serves the call.
type Sync struct {
	push    command.Usecase[pushusecase.Input, pushusecase.Output]
	pull    command.Usecase[pullusecase.Input, pullusecase.Output]
	changes *changes.Hub
	poll    time.Duration
}

// New returns the controller over the two use cases, the hub that wakes it and
// the interval it falls back on.
func New(
	push command.Usecase[pushusecase.Input, pushusecase.Output],
	pull command.Usecase[pullusecase.Input, pullusecase.Output],
	hub *changes.Hub,
	poll time.Duration,
) *Sync {
	return &Sync{push: push, pull: pull, changes: hub, poll: poll}
}

// Handle runs one stream until the device closes it or the node stops.
func (s *Sync) Handle(stream quirev1.SyncService_SyncServer) error {
	ctx := stream.Context()

	identity, err := authn.Require(ctx)
	if err != nil {
		return err
	}

	requests, failures := receive(ctx, stream)

	session := &session{
		controller: s,
		stream:     stream,
		reader:     identity.UserID,
		device:     identity.DeviceID,
	}

	return session.run(ctx, requests, failures)
}

// receive reads the stream into a channel, so that the loop below can wait on a
// message, a hint and a tick at once.
//
// A gRPC stream has no select-able receive, and a loop that called Recv
// directly could not be woken by anything else — which is the whole of what
// this stream is for.
func receive(
	ctx context.Context, stream quirev1.SyncService_SyncServer,
) (requests <-chan *quirev1.SyncRequest, failures <-chan error) {
	received := make(chan *quirev1.SyncRequest)
	failed := make(chan error, 1)

	go func() {
		defer close(received)

		for {
			request, err := stream.Recv()
			if err != nil {
				failed <- err

				return
			}

			select {
			case received <- request:
			case <-ctx.Done():
				return
			}
		}
	}()

	return received, failed
}

// session is one open stream and the two positions that describe it.
type session struct {
	controller *Sync
	stream     quirev1.SyncService_SyncServer
	reader     uuid.UUID
	device     uuid.UUID

	// sent is the highest position written to the socket.
	sent int64
	// acked is the highest position the device says it has stored. The gap
	// between the two is what is in flight, and what the window bounds.
	acked int64
}

// run drains the backlog and then serves the stream until it ends.
func (s *session) run(
	ctx context.Context, requests <-chan *quirev1.SyncRequest, failures <-chan error,
) error {
	if err := s.start(ctx, requests, failures); err != nil {
		return err
	}

	woken, release := s.controller.changes.Watch(s.reader)
	defer release()

	ticker := time.NewTicker(s.controller.poll)
	defer ticker.Stop()

	if err := s.drain(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case err := <-failures:
			return closed(err)
		case request, open := <-requests:
			if !open {
				return nil
			}

			if err := s.serve(ctx, request); err != nil {
				return err
			}
		case <-woken:
			if err := s.drain(ctx); err != nil {
				return err
			}
		case <-ticker.C:
			if err := s.drain(ctx); err != nil {
				return err
			}
		}
	}
}

// start reads the first message, which the contract requires to be the cursor.
//
// A stream that began with a push would be a device asking this node to store
// changes without saying where it had got to, and the node would have nothing
// to send it back.
func (s *session) start(
	ctx context.Context, requests <-chan *quirev1.SyncRequest, failures <-chan error,
) error {
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case err := <-failures:
		return closed(err)
	case request, open := <-requests:
		if !open {
			return nil
		}

		begin := request.GetStart()
		if begin == nil {
			return errs.New(errs.KindInvalidArgument, "the stream must begin with its cursor").
				WithOp(opHandle).
				WithCode(CodeNoStart)
		}

		s.sent, s.acked = begin.GetAfterPosition(), begin.GetAfterPosition()

		return nil
	}
}

// serve handles one message from the device.
func (s *session) serve(ctx context.Context, request *quirev1.SyncRequest) error {
	switch payload := request.GetPayload().(type) {
	case *quirev1.SyncRequest_Push:
		return s.accept(ctx, payload.Push.GetOperations())
	case *quirev1.SyncRequest_Ack:
		return s.acknowledge(ctx, payload.Ack.GetPosition())
	case *quirev1.SyncRequest_Start:
		// The cursor is the beginning of a stream and not a thing to reset in
		// the middle of one: a device that wants to start again has a stream
		// to open.
		return errs.New(errs.KindInvalidArgument, "the stream has already begun").
			WithOp(opHandle).
			WithCode(CodeRestarted)
	default:
		return errs.New(errs.KindInvalidArgument, "the message carries nothing to act on").
			WithOp(opHandle).
			WithCode(CodeEmptyRequest)
	}
}

// accept stores what the device pushed and answers with the verdicts.
//
// It does not drain afterwards. The push announces itself through the hub, and
// this stream is one of the listeners it wakes — so draining here would be the
// same read twice, and the hint is what the rest of the loop is written
// against.
func (s *session) accept(ctx context.Context, offered []*quirev1.Operation) error {
	changed, err := convert.Changes(offered)
	if err != nil {
		return err
	}

	output, err := s.controller.push.Execute(ctx, pushusecase.Input{
		UserID:     s.reader,
		Author:     s.device,
		Operations: changed,
	})
	if err != nil {
		return err
	}

	return s.stream.Send(&quirev1.SyncResponse{
		Payload: &quirev1.SyncResponse_PushResult{
			PushResult: &quirev1.SyncPushResult{Results: convert.Results(output.Results)},
		},
	})
}

// acknowledge records what the device has stored and sends what the window now
// allows.
//
// A confirmation of something never sent is ignored rather than refused. It
// costs nothing to be wrong about, and the alternative — ending a stream over
// a number — would cost a device its session for a bookkeeping mistake it can
// recover from on its own.
func (s *session) acknowledge(ctx context.Context, position int64) error {
	if position > s.acked && position <= s.sent {
		s.acked = position
	}

	return s.drain(ctx)
}

// drain sends what the device has not seen, a page at a time, while the window
// allows.
func (s *session) drain(ctx context.Context) error {
	for s.sent-s.acked < maxInFlight {
		output, err := s.controller.pull.Execute(ctx, pullusecase.Input{
			UserID:        s.reader,
			AfterPosition: s.sent,
		})
		if err != nil {
			return err
		}

		if len(output.Operations) == 0 {
			return nil
		}

		batch := &quirev1.OperationBatch{Operations: convert.Operations(output.Operations)}

		if err := s.stream.Send(&quirev1.SyncResponse{
			Payload: &quirev1.SyncResponse_Operations{Operations: batch},
		}); err != nil {
			return err
		}

		s.sent = output.LastPosition

		if !output.HasMore {
			return nil
		}
	}

	logging.From(ctx).DebugContext(ctx, "a device has not confirmed what it was sent",
		slog.String(logging.KeyDeviceID, s.device.String()),
		slog.Int64("sent", s.sent), slog.Int64("acknowledged", s.acked))

	return nil
}

// closed turns the end of a stream into the answer the node gives.
//
// A device that half-closed is a device that finished, and is not a failure. A
// cancellation is the device hanging up, which is the ordinary end of a stream
// nobody closed politely.
func closed(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case status.Code(err) == codes.Canceled:
		return nil
	default:
		return err
	}
}
