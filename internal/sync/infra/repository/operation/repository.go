// Package operation is the PostgreSQL adapter of the log repository: it
// satisfies the port declared in internal/sync/domain/operation and is the
// only place that knows sync.operations and sync.streams exist.
package operation

import (
	"context"
	"encoding/json"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/persist/syncdb"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opAppend   = "sync/operation: append"
	opList     = "sync/operation: list"
	opListByID = "sync/operation: list by id"
	opHead     = "sync/operation: head"
)

// Repository reads and writes the log, in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ operation.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *syncdb.Queries {
	return syncdb.New(r.manager.Executor(ctx))
}

// Append stores an operation and stamps it with the position this node
// allocated, reporting false when this node already had it.
//
// The duplicate arrives as an empty result rather than as an error, because
// the statement is an ON CONFLICT DO NOTHING with a RETURNING clause: a
// conflict writes nothing and therefore returns nothing. Translating that into
// a false here is what keeps the outcome the contract reports — a duplicate is
// the normal shape of a federation and not a fault — out of the use case's
// error path.
func (r *Repository) Append(ctx context.Context, op *operation.Operation) (bool, error) {
	delta, err := json.Marshal(op.Delta)
	if err != nil {
		return false, errs.Wrap(err, errs.KindInvalidArgument, "the change could not be stored").
			WithOp(opAppend).
			WithCode(operation.CodeInvalidDelta)
	}

	position, err := r.queries(ctx).AppendOperation(ctx, syncdb.AppendOperationParams{
		ID:           op.ID,
		UserID:       op.UserID,
		DeviceID:     op.DeviceID,
		TargetEntity: op.Target.Entity.String(),
		TargetID:     op.Target.ID,
		Operation:    op.Kind.String(),
		Delta:        delta,
		VectorClock:  op.VectorClock.Compact(),
		CreatedAt:    op.CreatedAt,
	})
	if err != nil {
		if persist.IsNoRows(err) {
			return false, nil
		}

		return false, persist.Classify(err, opAppend)
	}

	op.PlaceAt(position)

	return true, nil
}

// List reads one page of a reader's log and reports whether more remain.
//
// One more row than asked for is read, and the extra one is not returned. It
// is how the reply knows whether there is a next page without counting the
// whole log: a page that came back full might be the last one, and the only
// way to tell is to have looked one row past it.
func (r *Repository) List(
	ctx context.Context, query *operation.Query,
) ([]*operation.Operation, bool, error) {
	rows, err := r.queries(ctx).ListOperationsAfter(ctx, syncdb.ListOperationsAfterParams{
		UserID:        query.UserID,
		AfterPosition: query.AfterPosition,
		PageSize:      int32(query.Size) + 1,
	})
	if err != nil {
		return nil, false, persist.Classify(err, opList)
	}

	more := len(rows) > query.Size
	if more {
		rows = rows[:query.Size]
	}

	operations, err := toDomainAll(rows, opList)
	if err != nil {
		return nil, false, err
	}

	return operations, more, nil
}

// ListByID reads operations by the identifiers their authors minted.
func (r *Repository) ListByID(
	ctx context.Context, ids []uuid.UUID,
) ([]*operation.Operation, error) {
	if len(ids) == 0 {
		return []*operation.Operation{}, nil
	}

	rows, err := r.queries(ctx).ListOperationsByID(ctx, ids)
	if err != nil {
		return nil, persist.Classify(err, opListByID)
	}

	return toDomainAll(rows, opListByID)
}

// Head is this node's last allocated position for a reader.
func (r *Repository) Head(ctx context.Context, userID uuid.UUID) (int64, error) {
	head, err := r.queries(ctx).GetStreamHead(ctx, userID)
	if err != nil {
		return 0, persist.Classify(err, opHead)
	}

	return head, nil
}

// toDomainAll rebuilds every row of a page.
func toDomainAll(rows []syncdb.SyncOperation, op string) ([]*operation.Operation, error) {
	operations := make([]*operation.Operation, 0, len(rows))

	for index := range rows {
		stored, err := toDomain(&rows[index], op)
		if err != nil {
			return nil, err
		}

		operations = append(operations, stored)
	}

	return operations, nil
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one the authoring device minted.
//
// The value objects are cast rather than parsed, as they are in every other
// repository of this node and for a reason this slice feels first: an
// operation naming an entity a later version added is replicated back here,
// and a row this node can no longer parse must be merely unfamiliar rather
// than unreadable.
//
// The delta is the exception, and it is not a validation. It is decoded
// because the column holds bytes and the entity holds the fields the change
// claims, and a payload that is not an object could not have been written
// through this repository — sync.operations_delta_is_object refuses it.
func toDomain(row *syncdb.SyncOperation, op string) (*operation.Operation, error) {
	var delta operation.Delta
	if err := json.Unmarshal(row.Delta, &delta); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the stored change could not be read").
			WithOp(op).
			WithCode(operation.CodeInvalidDelta)
	}

	return operation.Restore(row.ID, &operation.Props{
		UserID:      row.UserID,
		DeviceID:    row.DeviceID,
		Position:    row.Position,
		Target:      operation.Target{Entity: operation.TargetEntity(row.TargetEntity), ID: row.TargetID},
		Kind:        operation.Kind(row.Operation),
		Delta:       delta,
		VectorClock: row.VectorClock,
		CreatedAt:   row.CreatedAt,
	}), nil
}
