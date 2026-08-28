// Package pushoperations is UC09 inbound: the node accepts a batch of changes
// authored elsewhere, stores what it did not already have, and reconciles each
// one against the record it names.
//
// It is the only use case in the node that two transports share. A device
// pushing what it wrote while disconnected (UC11) and a peer node replicating a
// reader it is authorized for (RF16) are offering the same thing, and the
// difference between them is who the caller is — which the controllers
// establish, one from a session and the other from a certificate, and hand over
// as an answer rather than as a credential.
//
// The unit of work is one operation, not the batch. A rejected change has to
// leave nothing behind while the changes around it stand, and PostgreSQL aborts
// a whole transaction on any statement it refuses — so a batch in one
// transaction would let one refused change take a reader's whole push with it.
package pushoperations

import (
	"context"
	"errors"
	"log/slog"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "sync/push: execute"

// CodeForgedAuthor is a batch containing a change the caller did not author,
// which RN10 refuses.
const CodeForgedAuthor = "forged_operation_author"

// PushOperations accepts changes authored elsewhere.
type PushOperations struct {
	log     operation.Repository
	records service.Records
	clock   service.Clock
	tx      service.Transaction
}

// PushOperations satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*PushOperations)(nil)

// New returns the use case over its dependencies.
func New(
	log operation.Repository,
	records service.Records,
	clock service.Clock,
	tx service.Transaction,
) *PushOperations {
	return &PushOperations{log: log, records: records, clock: clock, tx: tx}
}

// Execute stores and reconciles the batch, and answers with one verdict per
// change offered.
//
// RN10 is checked over the whole batch before anything is stored, and a batch
// containing one change the caller did not author fails whole rather than
// reporting a per-change rejection. That is the contract's own decision and it
// is the right one: a batch with a forged author in it is not a batch any of
// which should be trusted.
func (p *PushOperations) Execute(ctx context.Context, input Input) (Output, error) {
	if err := p.authorize(&input); err != nil {
		return Output{}, err
	}

	results := make([]operation.Result, 0, len(input.Operations))

	for index := range input.Operations {
		result, err := p.ingest(ctx, input.UserID, &input.Operations[index])
		if err != nil {
			return Output{}, err
		}

		results = append(results, result)
	}

	head, err := p.log.Head(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	return Output{Results: results, LastPosition: head}, nil
}

// authorize checks RN10 over the batch.
//
// A caller with no device is a peer node, and there is nothing to check: it
// replicates many devices and is none of them. What authorizes that call is the
// reader's own permission for the node, which its controller established
// before this was reached.
func (p *PushOperations) authorize(input *Input) error {
	if input.Author == (uuid.UUID{}) {
		return nil
	}

	for index := range input.Operations {
		if input.Operations[index].DeviceID != input.Author {
			return errs.New(errs.KindPermissionDenied,
				"the batch contains a change the caller did not author").
				WithOp(opExecute).
				WithCode(CodeForgedAuthor)
		}
	}

	return nil
}

// ingest stores and reconciles one change.
//
// The instant it carries is observed before anything is written, and observed
// whatever becomes of the change: a duplicate and a rejection are still
// instants this node has been told about, and the point of observing them is
// that a local write made afterwards is stamped after them (C01).
func (p *PushOperations) ingest(
	ctx context.Context, userID uuid.UUID, change *Change,
) (operation.Result, error) {
	op, err := parse(userID, change)
	if err != nil {
		return operation.Result{
			OperationID: change.ID,
			Verdict:     operation.Rejected(err.Error()),
		}, nil
	}

	if !p.clock.Observe(op.CreatedAt) {
		logging.From(ctx).WarnContext(ctx, "an operation was stamped too far ahead of this node",
			slog.String(logging.KeyDeviceID, op.DeviceID.String()),
			slog.Time("created_at", op.CreatedAt))
	}

	var verdict operation.Verdict

	err = p.tx.Within(ctx, func(ctx context.Context) error {
		stored, appended := p.log.Append(ctx, op)
		if appended != nil {
			return appended
		}

		if !stored {
			verdict = operation.Duplicate()

			return nil
		}

		reconciled, refused := p.records.Reconcile(ctx, op)
		if refused != nil {
			return refused
		}

		verdict = reconciled

		// A refused change leaves nothing behind, log entry included: the
		// contract stores what was superseded, because a later node has to
		// reach the same conclusion from the same history, and stores nothing
		// that it refused.
		if verdict.Outcome == operation.OutcomeRejected {
			return rejection{}
		}

		return nil
	})

	if err != nil && !errors.As(err, &rejection{}) {
		return operation.Result{}, err
	}

	return operation.Result{OperationID: op.ID, Verdict: verdict}, nil
}

// parse turns what the caller sent into the operation the entity takes.
func parse(userID uuid.UUID, change *Change) (*operation.Operation, error) {
	entity, err := operation.ParseTargetEntity(change.TargetEntity)
	if err != nil {
		return nil, err
	}

	kind, err := operation.ParseKind(change.Kind)
	if err != nil {
		return nil, err
	}

	return operation.New(change.ID, &operation.Props{
		UserID:      userID,
		DeviceID:    change.DeviceID,
		Target:      operation.Target{Entity: entity, ID: change.TargetID},
		Kind:        kind,
		Delta:       change.Delta,
		VectorClock: change.VectorClock,
		CreatedAt:   change.CreatedAt,
	})
}

// rejection unwinds the unit of work of a change the reconciler refused.
//
// It is a type rather than a sentinel value because the project's own linter
// forbids the package-level variable a sentinel would be, and because it says
// what it is where it is caught: this error never leaves the use case, and a
// caller that saw it would be seeing an implementation detail of a rollback.
type rejection struct{}

// Error renders the unwind.
func (rejection) Error() string { return "sync/push: unwinding a refused change" }
