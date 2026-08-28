// Package delivery is the PostgreSQL adapter of the delivery repository: it
// satisfies the port declared in internal/sync/domain/delivery and is the only
// place that knows sync.deliveries exists.
package delivery

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/delivery"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/persist/syncdb"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opEnqueue        = "sync/delivery: enqueue"
	opListPending    = "sync/delivery: list pending"
	opPendingServers = "sync/delivery: pending servers"
	opRecord         = "sync/delivery: record"
)

// Repository reads and writes what this node owes its peers, in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ delivery.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *syncdb.Queries {
	return syncdb.New(r.manager.Executor(ctx))
}

// Enqueue records that an operation is owed to each of the nodes.
//
// A reader with no authorized replica is the ordinary case, and it is not a
// call the caller has to guard: nothing to owe is nothing to write.
func (r *Repository) Enqueue(ctx context.Context, operationID uuid.UUID, servers []uuid.UUID) error {
	if len(servers) == 0 {
		return nil
	}

	err := r.queries(ctx).EnqueueDeliveries(ctx, syncdb.EnqueueDeliveriesParams{
		OperationID: operationID,
		ServerIds:   servers,
	})

	return persist.Classify(err, opEnqueue)
}

// ListPending reads what is still owed to one node.
func (r *Repository) ListPending(
	ctx context.Context, batch *delivery.Batch,
) ([]*delivery.Delivery, error) {
	rows, err := r.queries(ctx).ListPendingDeliveries(ctx, syncdb.ListPendingDeliveriesParams{
		ServerID:       batch.ServerID,
		Now:            batch.Now,
		BackoffSeconds: batch.Backoff.Seconds(),
		MaxExponent:    delivery.MaxBackoffExponent,
		PageSize:       int32(batch.Size),
	})
	if err != nil {
		return nil, persist.Classify(err, opListPending)
	}

	pending := make([]*delivery.Delivery, 0, len(rows))
	for index := range rows {
		pending = append(pending, toDomain(&rows[index]))
	}

	return pending, nil
}

// PendingServers reads the nodes this instance owes anything to at all.
func (r *Repository) PendingServers(ctx context.Context) ([]uuid.UUID, error) {
	servers, err := r.queries(ctx).ListPendingServers(ctx)
	if err != nil {
		return nil, persist.Classify(err, opPendingServers)
	}

	return servers, nil
}

// Record applies the outcome of one try to every delivery of the batch.
//
// The two statements are one method because they are one decision made
// elsewhere: the worker knows whether the peer answered, and a repository with
// a Confirm and a Fail would invite a caller to record a failure as a success
// by picking the wrong one.
func (r *Repository) Record(
	ctx context.Context, serverID uuid.UUID, operations []uuid.UUID, attempt *delivery.Attempt,
) (int64, error) {
	if len(operations) == 0 {
		return 0, nil
	}

	at := attempt.At.UTC()

	if attempt.Err == nil {
		rows, err := r.queries(ctx).ConfirmDeliveries(ctx, syncdb.ConfirmDeliveriesParams{
			AttemptedAt:  &at,
			ServerID:     serverID,
			OperationIds: operations,
		})

		return rows, persist.Classify(err, opRecord)
	}

	reason := attempt.Err.Error()

	rows, err := r.queries(ctx).FailDeliveries(ctx, syncdb.FailDeliveriesParams{
		AttemptedAt:  &at,
		Reason:       &reason,
		ServerID:     serverID,
		OperationIds: operations,
	})

	return rows, persist.Classify(err, opRecord)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one the column default minted when the
// operation was first owed.
func toDomain(row *syncdb.SyncDelivery) *delivery.Delivery {
	props := delivery.Props{
		OperationID:   row.OperationID,
		ServerID:      row.ServerID,
		AppliedAt:     row.AppliedAt,
		Attempts:      int(row.Attempts),
		LastAttemptAt: row.LastAttemptAt,
	}

	// Absent means there has been no failure to report, which is what a row
	// that has never been tried and one whose last try succeeded both look
	// like.
	if row.LastError != nil {
		props.LastError = *row.LastError
	}

	return delivery.Restore(row.ID, &props)
}
