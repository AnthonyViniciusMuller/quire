// Package convert translates between the messages of the sync contract and the
// vocabulary of the use cases.
//
// It is the one convert package in the node that reads causal metadata off the
// wire as well as writing it. Everywhere else the server stamps a revision and
// a client may only be told about it, which is what
// internal/shared/crdtpb says when it documents itself as going one way only —
// a client that could stamp its own would be able to claim it wrote before a
// write it has already seen. Here the causal state is the payload: an operation
// is what a device authored, clock and instant included, and a node that
// re-stamped it would be destroying the very history it is being asked to
// reconcile.
//
// Nothing here decides anything. A name outside an enumeration becomes the
// empty string rather than a default, so that the value object refuses it and
// the reply says which field was wrong — a change whose kind was guessed is a
// change nobody made.
package convert

import (
	"encoding/json"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/crdtpb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/identifier"
)

// opDecode is the operation reported by this file, in the form the errs package
// expects.
const opDecode = "sync/convert: decode"

// TargetEntity renders a stored entity name as the enumerator the contract
// names it by.
//
// A value this node does not know becomes UNSPECIFIED rather than an error, as
// it does in every other slice: adding a replicable entity is not a breaking
// change, so an operation replicated from a node running a later version keeps
// its row and can still be stored and returned — the only thing this node
// cannot do is name the entity to its own reader.
func TargetEntity(entity operation.TargetEntity) quirev1.TargetEntity {
	switch entity {
	case operation.TargetEbook:
		return quirev1.TargetEntity_TARGET_ENTITY_EBOOK
	case operation.TargetCollection:
		return quirev1.TargetEntity_TARGET_ENTITY_COLLECTION
	case operation.TargetEbookCollection:
		return quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION
	case operation.TargetReadingProgress:
		return quirev1.TargetEntity_TARGET_ENTITY_READING_PROGRESS
	case operation.TargetAnnotation:
		return quirev1.TargetEntity_TARGET_ENTITY_ANNOTATION
	default:
		return quirev1.TargetEntity_TARGET_ENTITY_UNSPECIFIED
	}
}

// TargetEntityValue reads an enumerator back into what is stored.
func TargetEntityValue(entity quirev1.TargetEntity) string {
	switch entity {
	case quirev1.TargetEntity_TARGET_ENTITY_EBOOK:
		return operation.TargetEbook.String()
	case quirev1.TargetEntity_TARGET_ENTITY_COLLECTION:
		return operation.TargetCollection.String()
	case quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION:
		return operation.TargetEbookCollection.String()
	case quirev1.TargetEntity_TARGET_ENTITY_READING_PROGRESS:
		return operation.TargetReadingProgress.String()
	case quirev1.TargetEntity_TARGET_ENTITY_ANNOTATION:
		return operation.TargetAnnotation.String()
	case quirev1.TargetEntity_TARGET_ENTITY_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// Kind renders what a change did to the record.
func Kind(kind operation.Kind) quirev1.OperationKind {
	switch kind {
	case operation.KindInsert:
		return quirev1.OperationKind_OPERATION_KIND_INSERT
	case operation.KindUpdate:
		return quirev1.OperationKind_OPERATION_KIND_UPDATE
	case operation.KindDelete:
		return quirev1.OperationKind_OPERATION_KIND_DELETE
	default:
		return quirev1.OperationKind_OPERATION_KIND_UNSPECIFIED
	}
}

// KindValue reads an enumerator back into what is stored.
func KindValue(kind quirev1.OperationKind) string {
	switch kind {
	case quirev1.OperationKind_OPERATION_KIND_INSERT:
		return operation.KindInsert.String()
	case quirev1.OperationKind_OPERATION_KIND_UPDATE:
		return operation.KindUpdate.String()
	case quirev1.OperationKind_OPERATION_KIND_DELETE:
		return operation.KindDelete.String()
	case quirev1.OperationKind_OPERATION_KIND_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// Outcome renders a verdict.
func Outcome(outcome operation.Outcome) quirev1.OperationOutcome {
	switch outcome {
	case operation.OutcomeApplied:
		return quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED
	case operation.OutcomeDuplicate:
		return quirev1.OperationOutcome_OPERATION_OUTCOME_DUPLICATE
	case operation.OutcomeSuperseded:
		return quirev1.OperationOutcome_OPERATION_OUTCOME_SUPERSEDED
	case operation.OutcomeRejected:
		return quirev1.OperationOutcome_OPERATION_OUTCOME_REJECTED
	case operation.OutcomeUnspecified:
		return quirev1.OperationOutcome_OPERATION_OUTCOME_UNSPECIFIED
	default:
		return quirev1.OperationOutcome_OPERATION_OUTCOME_UNSPECIFIED
	}
}

// Results renders the verdicts on a batch, in the order the changes were
// offered.
func Results(results []operation.Result) []*quirev1.OperationResult {
	rendered := make([]*quirev1.OperationResult, 0, len(results))

	for _, result := range results {
		rendered = append(rendered, &quirev1.OperationResult{
			OperationId: result.OperationID.String(),
			Outcome:     Outcome(result.Outcome),
			Detail:      result.Detail,
		})
	}

	return rendered
}

// Operation renders one change as it travels.
//
// The position is rendered, unlike in a request where it is ignored: it is this
// node's order for this reader's log, and it is the cursor the caller sends
// back.
func Operation(op *operation.Operation) *quirev1.Operation {
	return &quirev1.Operation{
		Id:           op.ID.String(),
		DeviceId:     op.DeviceID.String(),
		TargetEntity: TargetEntity(op.Target.Entity),
		TargetId:     op.Target.ID.String(),
		Operation:    Kind(op.Kind),
		Delta:        delta(op.Delta),
		VectorClock:  crdtpb.VectorClock(op.VectorClock),
		CreatedAt:    crdtpb.Timestamp(op.CreatedAt),
		Position:     op.Position,
	}
}

// Operations renders a page of the log.
func Operations(page []*operation.Operation) []*quirev1.Operation {
	rendered := make([]*quirev1.Operation, 0, len(page))
	for _, op := range page {
		rendered = append(rendered, Operation(op))
	}

	return rendered
}

// Changes reads a batch of offered changes into the vocabulary of the use case.
//
// A malformed identifier is refused here rather than being carried in as a zero
// value, because the zero value is what the entity refuses on behalf of a
// change that named nothing — and a client that sent "not-a-uuid" would be told
// its change named no record, which is true and unhelpful.
func Changes(offered []*quirev1.Operation) ([]pushoperations.Change, error) {
	changes := make([]pushoperations.Change, 0, len(offered))

	for _, message := range offered {
		change, err := Change(message)
		if err != nil {
			return nil, err
		}

		changes = append(changes, change)
	}

	return changes, nil
}

// Change reads one offered change.
func Change(message *quirev1.Operation) (pushoperations.Change, error) {
	id, err := identifier.Parse(message.GetId(), "id")
	if err != nil {
		return pushoperations.Change{}, err
	}

	device, err := identifier.Parse(message.GetDeviceId(), "device_id")
	if err != nil {
		return pushoperations.Change{}, err
	}

	target, err := identifier.Parse(message.GetTargetId(), "target_id")
	if err != nil {
		return pushoperations.Change{}, err
	}

	claimed, err := claims(message.GetDelta())
	if err != nil {
		return pushoperations.Change{}, err
	}

	return pushoperations.Change{
		ID:           id,
		DeviceID:     device,
		TargetEntity: TargetEntityValue(message.GetTargetEntity()),
		TargetID:     target,
		Kind:         KindValue(message.GetOperation()),
		Delta:        claimed,
		VectorClock:  VectorClock(message.GetVectorClock()),
		CreatedAt:    Timestamp(message.GetCreatedAt()),
	}, nil
}

// VectorClock reads a causal version off the wire.
//
// Zero entries are dropped, which the contract requires of a receiver in as
// many words: a device absent from the map and a device mapped to zero are the
// same causal history, and a node that kept both forms would make one history
// compare unequal to itself.
func VectorClock(clock *quirev1.VectorClock) crdt.VectorClock {
	entries := clock.GetEntries()

	read := make(crdt.VectorClock, len(entries))
	for device, counter := range entries {
		if counter > 0 {
			read[crdt.DeviceID(device)] = counter
		}
	}

	return read
}

// Timestamp reads a causal instant off the wire.
//
// An absent one is the zero instant and not the epoch, so that a change which
// did not say when it was authored is refused by the entity rather than
// arriving stamped in 1970 and winning nothing for ever.
func Timestamp(at *quirev1.HybridTimestamp) time.Time {
	if at == nil {
		return time.Time{}
	}

	return time.UnixMicro(at.GetUnixMicros()).UTC()
}

// delta renders the fields a change claims.
//
// The contract carries it as a Struct, which is a JSON object with a protobuf
// name, so the two forms are one encoding and the conversion is the encoding
// itself. A delta this node cannot render is rendered as empty rather than
// dropping the change: the row was written through a constraint that admits
// only an object, so there is nothing here that could fail on data this node
// stored.
func delta(claimed operation.Delta) *structpb.Struct {
	encoded, err := json.Marshal(claimed)
	if err != nil {
		return &structpb.Struct{}
	}

	rendered := &structpb.Struct{}
	if err = rendered.UnmarshalJSON(encoded); err != nil {
		return &structpb.Struct{}
	}

	return rendered
}

// claims reads the fields a change claims.
func claims(offered *structpb.Struct) (operation.Delta, error) {
	if offered == nil {
		return operation.Delta{}, nil
	}

	encoded, err := offered.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalidArgument, "the change could not be read").
			WithOp(opDecode).
			WithCode(operation.CodeInvalidDelta).
			WithField("delta", "it is not an object of changed fields")
	}

	var claimed operation.Delta
	if err = json.Unmarshal(encoded, &claimed); err != nil {
		return nil, errs.Wrap(err, errs.KindInvalidArgument, "the change could not be read").
			WithOp(opDecode).
			WithCode(operation.CodeInvalidDelta).
			WithField("delta", "it is not an object of changed fields")
	}

	return claimed, nil
}
