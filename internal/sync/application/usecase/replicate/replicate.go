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
// the far end, because the reconciler there creates records only from an
// insert — and a refusal is not final, which is the third decision.
//
// It tells the peer who the reader is before it offers anything of theirs.
// A replica holds nothing until its own tables name the reader and their
// devices (C22), and devices are bound after the authorization as often as
// before it, so the moment a reader's changes are about to reach a node is
// the moment to make sure the node can hold them. The adapter remembers what
// a node was told and calls it only when that changed.
//
// It records the outcome of every try, and the count is what the backoff is
// computed from. A peer belonging to another operator is unreachable often
// enough that retrying it at full rate would be this node's largest source of
// outbound traffic, and a failure nobody counted is a peer retried for ever. A
// refusal counts as a failed try too: what the peer refuses depends on what it
// already holds, so a change refused today for naming a record the peer has
// not received yet is a change the peer applies once it has.
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
	admissions service.Admissions
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
	admissions service.Admissions,
	clock service.Clock,
	backoff time.Duration,
	batchSize int,
) *Replicate {
	return &Replicate{
		deliveries: deliveries,
		log:        log,
		peers:      peers,
		admissions: admissions,
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
// A change the destination applied, already held or judged superseded is
// settled: in each of the three it has read the change and holds it. A change
// it refused stays owed, and the refusal is counted as a failed try with the
// peer's reason as the error. The destination's verdict is not a property of
// the change alone but of the change against what the destination holds, and
// that changes: an update refused because the record it names has not arrived
// is applied on the try after the insert lands. Settling it would have been
// this node claiming a delivery the peer never made, and nothing afterwards
// would have noticed. A refusal that never lifts costs one call per backoff
// interval, which is the price of a queue that never drops what it owes; the
// reason stays in last_error for the operator to act on.
//
// What the destination did not answer for at all is a call that was cut
// short rather than a verdict, and is counted the same way.
func (r *Replicate) offer(ctx context.Context, serverID uuid.UUID, batch *readerBatch) (pass, error) {
	attempted := r.clock.Now()

	// The peer is told who the reader is first, and a peer that could not be
	// told is a peer that would refuse the batch: the batch is counted as a
	// failed try rather than offered to a node that cannot hold it.
	err := r.admissions.Admit(ctx, serverID, batch.userID)
	if err == nil {
		var results []operation.Result

		results, err = r.peers.Replicate(ctx, serverID, batch.userID, batch.operations)
		if err == nil {
			return r.settle(ctx, serverID, batch, results, attempted)
		}
	}

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

// settle records what the peer answered about one reader's batch.
func (r *Replicate) settle(
	ctx context.Context, serverID uuid.UUID, batch *readerBatch, results []operation.Result, attempted time.Time,
) (pass, error) {
	answered := make([]uuid.UUID, 0, len(results))
	settled := make([]uuid.UUID, 0, len(results))
	refused := make([]operation.Result, 0)

	for _, result := range results {
		answered = append(answered, result.OperationID)

		if result.Outcome != operation.OutcomeRejected {
			settled = append(settled, result.OperationID)

			continue
		}

		logging.From(ctx).WarnContext(ctx, "a peer refused a change",
			slog.String("server_id", serverID.String()),
			slog.String("operation_id", result.OperationID.String()),
			slog.String("detail", result.Detail))

		refused = append(refused, result)
	}

	confirmed, err := r.deliveries.Record(ctx, serverID, settled,
		&delivery.Attempt{At: attempted})
	if err != nil {
		return pass{}, err
	}

	failed := int64(0)

	// One row at a time, because each refusal carries its own reason and the
	// reason is what the operator reads. A batch refused wholesale is rare
	// enough that the extra statements do not matter.
	for _, refusal := range refused {
		counted, recordErr := r.deliveries.Record(ctx, serverID, []uuid.UUID{refusal.OperationID},
			&delivery.Attempt{At: attempted, Err: refusedError{detail: refusal.Detail}})
		if recordErr != nil {
			return pass{}, recordErr
		}

		failed += counted
	}

	silent := missing(batch.identifiers(), answered)
	if len(silent) == 0 {
		return pass{Confirmed: confirmed, Failed: failed}, nil
	}

	counted, err := r.deliveries.Record(ctx, serverID, silent, &delivery.Attempt{
		At:  attempted,
		Err: noVerdictError{},
	})
	if err != nil {
		return pass{}, err
	}

	return pass{Confirmed: confirmed, Failed: failed + counted}, nil
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

// refusedError is what a delivery the destination refused is recorded with:
// the peer's own reason, which is the one thing about the refusal the operator
// can act on.
type refusedError struct{ detail string }

// Error renders the refusal.
func (e refusedError) Error() string { return "the destination refused the change: " + e.detail }
