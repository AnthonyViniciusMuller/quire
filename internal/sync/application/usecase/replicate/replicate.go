// Package replicate is one pass of the delivery queue: UC09 between nodes
// rather than between a device and its node (RF16, RN03).
//
// Replication is driven from the side that owes the data, and this is that
// side. There is no peer-facing pull in the contract because a peer that was
// unreachable for a week has to be caught up by something that remembers what
// it missed, and remembering is what sync.deliveries does.
//
// The pass has three steps and each is a decision.
//
// It fills the queue from the log rather than from the call that stored the
// change. A node authorized as a replica today holds none of the reader's
// history, and rows written when the change happened would leave it
// permanently missing everything from before its own authorization. Filling
// from the log makes a new authorization and a week of downtime the same case.
//
// It offers a reader's changes in the order this node committed them. A batch
// that carried an update ahead of the insert it depends on would be refused at
// the far end, and the refusal would be permanent: the reconciler there creates
// records only from an insert.
//
// It records the outcome of every try, and the count is what the backoff is
// computed from. A peer belonging to another operator is unreachable often
// enough that retrying it at full rate would be this node's largest source of
// outbound traffic, and a failure nobody counted is a peer retried for ever.
package replicate

import (
	"context"
	"log/slog"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/delivery"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Replicate drains what this node owes its peers.
type Replicate struct {
	deliveries delivery.Repository
	log        operation.Repository
	peers      service.Peers
	clock      service.Clock
	backoff    time.Duration
	batchSize  int
}

// Replicate satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*Replicate)(nil)

// New returns the use case over its dependencies and the two numbers the
// deployment sets.
func New(
	deliveries delivery.Repository,
	log operation.Repository,
	peers service.Peers,
	clock service.Clock,
	backoff time.Duration,
	batchSize int,
) *Replicate {
	return &Replicate{
		deliveries: deliveries,
		log:        log,
		peers:      peers,
		clock:      clock,
		backoff:    backoff,
		batchSize:  batchSize,
	}
}

// Execute runs one pass of the queue.
//
// A peer that could not be reached does not end the pass. Peers belong to
// different operators and go down independently, so one that is unreachable is
// a fact about that peer: the pass records the failure, backs it off, and goes
// on to the next.
func (r *Replicate) Execute(ctx context.Context, _ Input) (Output, error) {
	enqueued, err := r.deliveries.EnqueuePending(ctx)
	if err != nil {
		return Output{}, err
	}

	servers, err := r.deliveries.PendingServers(ctx)
	if err != nil {
		return Output{}, err
	}

	output := Output{Enqueued: enqueued, Servers: len(servers)}

	for _, serverID := range servers {
		pass, err := r.drain(ctx, serverID)
		if err != nil {
			return Output{}, err
		}

		output.Offered += pass.Offered
		output.Confirmed += pass.Confirmed
		output.Failed += pass.Failed
	}

	return output, nil
}

// drain offers one peer as much of what it is owed as a batch carries.
func (r *Replicate) drain(ctx context.Context, serverID uuid.UUID) (Output, error) {
	pending, err := r.deliveries.ListPending(ctx, &delivery.Batch{
		ServerID: serverID,
		Now:      r.clock.Now(),
		Backoff:  r.backoff,
		Size:     r.batchSize,
	})
	if err != nil {
		return Output{}, err
	}

	if len(pending) == 0 {
		return Output{}, nil
	}

	owed := make([]uuid.UUID, 0, len(pending))
	for _, row := range pending {
		owed = append(owed, row.OperationID)
	}

	// The log is read in reader and position order, which is the order the
	// batches below are cut in. A peer is offered a reader's history the way
	// this node committed it.
	operations, err := r.log.ListByID(ctx, owed)
	if err != nil {
		return Output{}, err
	}

	output := Output{}

	for _, batch := range byReader(operations) {
		offered, err := r.offer(ctx, serverID, batch)
		if err != nil {
			return Output{}, err
		}

		output.Offered += len(batch.operations)
		output.Confirmed += offered.Confirmed
		output.Failed += offered.Failed

		// A peer that could not be reached will not be reachable for the
		// reader after this one either, and trying is what the backoff exists
		// to stop.
		if offered.unreachable {
			break
		}
	}

	return output, nil
}

// pass is what offering one reader's batch to one peer did.
type pass struct {
	Confirmed   int64
	Failed      int64
	unreachable bool
}

// offer hands one reader's changes to one peer and settles the rows.
//
// Every change the destination answered about is settled, whatever it answered.
// A refusal is a verdict and not a delivery failure: the destination has read
// the change and will read it the same way again, so a queue that retried it
// would never drain and the operator would be told about the same refusal for
// ever. What is retried is what the destination did not answer for, which is a
// call that was lost rather than a change that was refused.
func (r *Replicate) offer(ctx context.Context, serverID uuid.UUID, batch *readerBatch) (pass, error) {
	attempted := r.clock.Now()

	results, err := r.peers.Replicate(ctx, serverID, batch.userID, batch.operations)
	if err != nil {
		logging.From(ctx).WarnContext(ctx, "a peer could not be offered what it is owed",
			slog.String("server_id", serverID.String()),
			slog.Int("owed", len(batch.operations)), logging.Err(err))

		failed, recordErr := r.deliveries.Record(ctx, serverID, batch.identifiers(),
			&delivery.Attempt{At: attempted, Err: err})
		if recordErr != nil {
			return pass{}, recordErr
		}

		return pass{Failed: failed, unreachable: true}, nil
	}

	answered := make([]uuid.UUID, 0, len(results))

	for _, result := range results {
		answered = append(answered, result.OperationID)

		if result.Outcome == operation.OutcomeRejected {
			logging.From(ctx).WarnContext(ctx, "a peer refused a change",
				slog.String("server_id", serverID.String()),
				slog.String("operation_id", result.OperationID.String()),
				slog.String("detail", result.Detail))
		}
	}

	confirmed, err := r.deliveries.Record(ctx, serverID, answered,
		&delivery.Attempt{At: attempted})
	if err != nil {
		return pass{}, err
	}

	silent := missing(batch.identifiers(), answered)
	if len(silent) == 0 {
		return pass{Confirmed: confirmed}, nil
	}

	failed, err := r.deliveries.Record(ctx, serverID, silent, &delivery.Attempt{
		At:  attempted,
		Err: noVerdictError{},
	})
	if err != nil {
		return pass{}, err
	}

	return pass{Confirmed: confirmed, Failed: failed}, nil
}

// readerBatch is one reader's changes, in the order this node committed them.
type readerBatch struct {
	userID     uuid.UUID
	operations []*operation.Operation
}

// identifiers is the changes the batch carries.
func (b *readerBatch) identifiers() []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(b.operations))
	for _, op := range b.operations {
		ids = append(ids, op.ID)
	}

	return ids
}

// byReader cuts the batch into one per reader, keeping the order within each.
//
// The peer-facing call names one reader, because a certificate identifies the
// node and not any of the readers it replicates, so a batch that crossed
// readers could not be sent at all.
func byReader(operations []*operation.Operation) []*readerBatch {
	batches := make([]*readerBatch, 0, 1)
	index := map[uuid.UUID]*readerBatch{}

	for _, op := range operations {
		batch, open := index[op.UserID]
		if !open {
			batch = &readerBatch{userID: op.UserID}
			index[op.UserID] = batch
			batches = append(batches, batch)
		}

		batch.operations = append(batch.operations, op)
	}

	return batches
}

// missing is what the destination did not answer for.
func missing(offered, answered []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(answered))
	for _, id := range answered {
		seen[id] = struct{}{}
	}

	silent := make([]uuid.UUID, 0, len(offered))

	for _, id := range offered {
		if _, ok := seen[id]; !ok {
			silent = append(silent, id)
		}
	}

	return silent
}

// noVerdictError is what a delivery the destination did not mention is recorded
// with.
//
// It is a type rather than a sentinel value because the project's own linter
// forbids the package-level variable a sentinel would be, and it says what the
// operator will read in last_error.
type noVerdictError struct{}

// Error renders the silence.
func (noVerdictError) Error() string { return "the destination answered nothing about this change" }
