// Package delivery is what this node still owes one peer: the entity, and the
// port a repository has to satisfy.
//
// It exists because C07 splits it out of operacao_sync. The specification put
// the destination node and the instant it applied the change on the operation
// itself, which are properties of a delivery and not of a change: one change
// destined for three authorized replicas would be three rows each carrying its
// own copy of the delta, and the same change would then have three different
// identifiers — which makes deduplication at the receiving end depend on
// comparing payloads rather than on the identifier its author minted.
//
// Split, the delta is stored once and the operation keeps one identity across
// the federation. What this entity carries is the queue state: the operation,
// the destination, whether it has been confirmed, and what a worker needs in
// order to back off — the number of attempts, the instant of the last one and
// the last error. A peer belonging to another operator is unreachable often
// enough that retrying it at full rate would be this node's largest source of
// outbound traffic, and backing off requires that state to be durable.
//
// Replication is driven from the side that owes the data, which is why there
// is a queue here at all and no peer-facing pull in the contract: a peer that
// was unreachable for a week is caught up by these rows rather than by asking.
package delivery

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by this file, in the form the errs package
// expects.
const opNew = "sync/delivery: new"

// CodeInvalidDelivery is a delivery that could not be recorded.
const CodeInvalidDelivery = "invalid_delivery"

// Attempt is one try at handing an operation to a peer, as the worker reports
// it back.
type Attempt struct {
	// At is when the try was made.
	At time.Time
	// Err is why it failed, nil when the peer confirmed.
	Err error
}

// Props is everything about a delivery other than its identifier.
type Props struct {
	// OperationID is the change owed.
	OperationID uuid.UUID

	// ServerID is the node it is owed to. It is the catalogue's identifier for
	// the peer and not its domain, because a peer that changed the authority
	// it answers on is still the peer this node was replicating to.
	ServerID uuid.UUID

	// AppliedAt is when the destination confirmed it, nil while it is still
	// owed. It is what makes this table a queue rather than a history.
	AppliedAt *time.Time

	// Attempts is how many times the worker has tried, and the exponent the
	// backoff is computed from.
	Attempts int

	// LastAttemptAt is when it last tried, nil before the first try.
	LastAttemptAt *time.Time

	// LastError is why the last try failed, empty when there has been none or
	// when the last one succeeded. It is for the operator and never for the
	// reader: a peer belonging to somebody else fails in ways only its
	// operator can act on.
	LastError string
}

// Delivery is one operation owed to one node (MER: entrega_sync;
// sync.deliveries).
type Delivery struct {
	// ID is the row's own identifier. The pair the row is addressed by is
	// (OperationID, ServerID), which the schema makes unique; this exists so
	// that the row can be named at all.
	ID uuid.UUID

	Props
}

// New records that an operation is owed to a node.
//
// The row is created already pending, which is the only state it can be
// created in: what makes it a delivery is that it has not happened yet.
func New(operationID, serverID uuid.UUID) (*Delivery, error) {
	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the delivery could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidDelivery).
			WithField(field, reason)
	}

	switch {
	case operationID == (uuid.UUID{}):
		return nil, invalid("operation_id", "a delivery must name the change it owes")
	case serverID == (uuid.UUID{}):
		return nil, invalid("server_id", "a delivery must name the node it owes it to")
	}

	return &Delivery{
		ID:    uuid.New(),
		Props: Props{OperationID: operationID, ServerID: serverID},
	}, nil
}

// Restore rebuilds a delivery already stored, without minting an identifier.
func Restore(id uuid.UUID, props *Props) *Delivery {
	return &Delivery{ID: id, Props: *props}
}

// IsPending reports whether the destination has still not confirmed the
// operation.
func (d *Delivery) IsPending() bool { return d.AppliedAt == nil }

// Record applies the outcome of one try.
//
// A confirmation is final: the row stops being part of the queue and the last
// error is cleared, because a delivery that succeeded is not a delivery that
// failed a while ago. A failure only counts the try, and the count is what the
// backoff is computed from — which is why the worker must record a failure
// rather than merely logging it.
func (d *Delivery) Record(attempt *Attempt) {
	at := attempt.At.UTC()

	d.Attempts++
	d.LastAttemptAt = &at

	if attempt.Err != nil {
		d.LastError = attempt.Err.Error()

		return
	}

	d.AppliedAt = &at
	d.LastError = ""
}
