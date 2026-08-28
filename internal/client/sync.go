package client

import (
	"context"
	"errors"
	"io"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// PushReport is what the node did with the changes this device offered.
type PushReport struct {
	// Results is one verdict per change, in the order they were offered.
	Results []*quirev1.OperationResult

	// LastPosition is this node's head for the reader after the push, which is
	// how a device that has just pushed learns whether there is anything to
	// pull without asking.
	LastPosition int64
}

// PullReport is a page of the log.
type PullReport struct {
	Operations []*quirev1.Operation

	// LastPosition is the position of the last operation returned, and the
	// cursor to send next time. It is not the node's head: with HasMore there
	// is more behind it.
	LastPosition int64

	HasMore bool
}

// Watcher is what a caller of [Client.Watch] is told, as it happens.
type Watcher struct {
	// Operations is called with each batch the node sends, after the client
	// has folded it into what this device knows.
	Operations func([]*quirev1.Operation)

	// PushResults is called with the verdicts on the changes the stream
	// carried, which arrive separately from the operations.
	PushResults func([]*quirev1.OperationResult)
}

// Cursor is the last position this device has taken from the node it is
// talking to.
//
// It is per node because the position is that node's own order for the reader's
// log: two nodes number the same operations differently, and a device that
// pulls from both keeps a number for each.
func (c *Client) Cursor() int64 { return c.state.Cursors[c.Address()] }

// Push hands the node everything this device authored while it could not reach
// it, in the order it was authored (UC09, UC11).
//
// A change the node answered leaves the log whatever the answer was. Applied,
// duplicate and superseded are all the node having it; a rejection is the one
// answer that loses the change, and it is kept out of the log for the reason
// the contract gives — a caller cannot fix most of them by retrying, and a
// change that stayed would fail the same way at every push from now on. It is
// returned, so that the reader is told rather than the change disappearing
// quietly.
func (c *Client) Push(ctx context.Context) (PushReport, error) {
	if err := c.requireOnline("push"); err != nil {
		return PushReport{}, err
	}

	if len(c.state.Pending) == 0 {
		return PushReport{}, nil
	}

	offered, err := c.offer()
	if err != nil {
		return PushReport{}, err
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return PushReport{}, err
	}

	response, err := c.sync.PushOperations(authorized, &quirev1.PushOperationsRequest{Operations: offered})
	if err != nil {
		return PushReport{}, err
	}

	c.settle(response.GetResults())

	if err := c.save(); err != nil {
		return PushReport{}, err
	}

	return PushReport{Results: response.GetResults(), LastPosition: response.GetLastPosition()}, nil
}

// offer renders the pending log as the messages the contract carries.
func (c *Client) offer() ([]*quirev1.Operation, error) {
	offered := make([]*quirev1.Operation, 0, len(c.state.Pending))

	for _, queued := range c.state.Pending {
		message, err := queued.message()
		if err != nil {
			return nil, err
		}

		offered = append(offered, message)
	}

	return offered, nil
}

// settle drops from the log every change the node answered.
//
// A change the reply does not mention stays. The node answers one result per
// change offered, so that case is a node that answered a shorter batch than it
// was given, and keeping what it did not name is the only reading that cannot
// lose a change.
func (c *Client) settle(results []*quirev1.OperationResult) {
	answered := make(map[uuid.UUID]struct{}, len(results))
	for _, result := range results {
		answered[parseID(result.GetOperationId())] = struct{}{}
	}

	remaining := make([]Operation, 0, len(c.state.Pending))

	for _, queued := range c.state.Pending {
		if _, done := answered[queued.ID]; !done {
			remaining = append(remaining, queued)
		}
	}

	c.state.Pending = remaining
}

// Pull collects everything the node holds after this device's cursor, in order
// (RN06).
//
// The page includes changes this device authored, which costs it one comparison
// and is what keeps the cursor meaning "everything this node holds below here":
// a page that hid them would leave gaps a device could not tell from gaps
// nobody told it about.
//
// An empty page leaves the cursor where it was. Answering it with a zero would
// send a device that had drained the log back to its beginning.
func (c *Client) Pull(ctx context.Context, limit int32) (PullReport, error) {
	authorized, err := c.call(ctx, "pull")
	if err != nil {
		return PullReport{}, err
	}

	response, err := c.sync.PullOperations(authorized, &quirev1.PullOperationsRequest{
		AfterPosition: c.Cursor(),
		Limit:         limit,
	})
	if err != nil {
		return PullReport{}, err
	}

	operations := response.GetOperations()

	c.absorb(operations)

	if len(operations) > 0 {
		c.state.Cursors[c.Address()] = response.GetLastPosition()
	}

	if err := c.save(); err != nil {
		return PullReport{}, err
	}

	return PullReport{
		Operations:   operations,
		LastPosition: response.GetLastPosition(),
		HasMore:      response.GetHasMore(),
	}, nil
}

// Watch keeps the stream open: the backlog first, then what happens next, and
// this device's own changes in the other direction (UC10, UC11).
//
// It returns when the context is cancelled or the node closes the stream. The
// acknowledgement is what keeps it flowing — the node sends one page and does
// not send again until the device has confirmed what it already has, because an
// operation written to a socket is not an operation written to disk.
func (c *Client) Watch(ctx context.Context, watcher Watcher) error {
	authorized, err := c.call(ctx, "sync")
	if err != nil {
		return err
	}

	stream, err := c.sync.Sync(authorized)
	if err != nil {
		return err
	}

	// A send that fails with io.EOF is a stream the node has already closed,
	// and the reason is on the receive: the loop below is what reports it,
	// rather than this returning a failure that says only that the stream
	// ended.
	if err = stream.Send(&quirev1.SyncRequest{
		Payload: &quirev1.SyncRequest_Start{Start: &quirev1.SyncStart{AfterPosition: c.Cursor()}},
	}); err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	if len(c.state.Pending) > 0 {
		offered, offerErr := c.offer()
		if offerErr != nil {
			return offerErr
		}

		if err = stream.Send(&quirev1.SyncRequest{
			Payload: &quirev1.SyncRequest_Push{Push: &quirev1.OperationBatch{Operations: offered}},
		}); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}

	return c.follow(stream, watcher)
}

// follow reads the stream until it ends.
//
// Everything is sent from this loop and nothing from a second goroutine, which
// is what makes the acknowledgement a reply rather than a race: the client
// confirms a page after it has stored it, and stores nothing while it is
// confirming.
func (c *Client) follow(stream quirev1.SyncService_SyncClient, watcher Watcher) error {
	for {
		message, err := stream.Recv()

		switch {
		case errors.Is(err, io.EOF), status.Code(err) == codes.Canceled:
			return nil
		case err != nil:
			return err
		}

		if results := message.GetPushResult(); results != nil {
			c.settle(results.GetResults())

			if failure := c.save(); failure != nil {
				return failure
			}

			if watcher.PushResults != nil {
				watcher.PushResults(results.GetResults())
			}

			continue
		}

		operations := message.GetOperations().GetOperations()
		if len(operations) == 0 {
			continue
		}

		c.absorb(operations)

		position := operations[len(operations)-1].GetPosition()
		c.state.Cursors[c.Address()] = position

		if failure := c.save(); failure != nil {
			return failure
		}

		if watcher.Operations != nil {
			watcher.Operations(operations)
		}

		if err = stream.Send(&quirev1.SyncRequest{
			Payload: &quirev1.SyncRequest_Ack{Ack: &quirev1.SyncAck{Position: position}},
		}); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
}

// absorb folds a page of the log into what this device knows.
//
// Two things happen per operation and both are what lets the next change this
// device authors be reconcilable. The instant is observed, so that what this
// device stamps next is stamped after what it has just seen. And the causal
// version is merged into the version this device holds of that record, so that
// a later write ticks over a history that includes this one instead of claiming
// to have been made without knowing about it.
func (c *Client) absorb(operations []*quirev1.Operation) {
	for _, op := range operations {
		c.observe(op.GetCreatedAt())

		key, id, addressed := c.addressOf(op)
		if !addressed {
			continue
		}

		c.remember(key, id, readClock(op.GetVectorClock()))
	}
}

// addressOf is the key an operation's record is remembered under, and whether
// this device has any business remembering it.
//
// Three of the five records are addressed by the identifier the operation
// names. The filing of a work under a grouping is addressed by the pair in its
// delta, because the row's own identifier is one each replica minted for itself
// (C18). And a reading position is addressed by the work — the device is the
// other half of its key, and a position another device wrote is one this device
// can never author a change to, so it is skipped rather than remembered under a
// clock it has no way to tick.
func (c *Client) addressOf(op *quirev1.Operation) (key string, id uuid.UUID, addressed bool) {
	entity := entityName(op.GetTargetEntity())
	target := parseID(op.GetTargetId())

	switch entity {
	case entityEbook, entityCollection, entityAnnotation:
		return recordKey(entity, target), target, true
	case entityFiling:
		work := parseID(field(op, fieldEbookID))
		grouping := parseID(field(op, fieldCollectionID))

		if work == (uuid.UUID{}) || grouping == (uuid.UUID{}) {
			return "", uuid.UUID{}, false
		}

		filing := recordKey(entityFiling, work, grouping)

		return filing, c.target(filing), true
	case entityPosition:
		if parseID(op.GetDeviceId()) != c.state.Device.ID {
			return "", uuid.UUID{}, false
		}

		work := parseID(field(op, fieldEbookID))
		if work == (uuid.UUID{}) {
			return "", uuid.UUID{}, false
		}

		position := recordKey(entityPosition, work)

		return position, c.target(position), true
	default:
		return "", uuid.UUID{}, false
	}
}

// field reads a string the delta claims, or the empty string when it claims
// none.
func field(op *quirev1.Operation, name string) string {
	value, claimed := op.GetDelta().GetFields()[name]
	if !claimed {
		return ""
	}

	return value.GetStringValue()
}
