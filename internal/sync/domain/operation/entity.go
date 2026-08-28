// Package operation is one change to one record as it travels between
// replicas: the entity, the value objects that describe what it changed, and
// the port a repository has to satisfy.
//
// It is the entity the whole replication mechanism turns on (MER:
// operacao_sync; sync.operations), and three properties shape every type in
// the package.
//
// The identifier is minted by the device that authored the change and is the
// same uuid on every node that ever sees it. A node receiving the same
// operation twice — from the device, and again from a peer that also
// replicates the reader — recognizes it by that identifier, so receiving is
// idempotent and deduplication never has to compare payloads. Nothing here
// generates one: [New] takes it, unlike every other entity in this node.
//
// The position is node-local and is not part of the operation as it travels.
// It is this node's order for this reader's log, allocated when the row is
// written here, and two nodes will number the same operations differently. It
// is therefore not a parameter of [New] but a value [Operation.PlaceAt] stamps
// once the row exists, and the contract says the same thing when it declares
// the field ignored in a request.
//
// The log is append-only. An operation is never edited after it is written, so
// this entity has no method that changes what it says; what changes is the
// delivery rows that point at it, which are
// [github.com/anthonyvsmuller/quire/internal/sync/domain/delivery].
package operation

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by this file, in the form the errs package
// expects.
const opNew = "sync/operation: new"

// CodeInvalidOperation is a change that could not be recorded for a reason
// none of the value objects owns.
const CodeInvalidOperation = "invalid_operation"

// Props is everything about an operation other than its identifier.
type Props struct {
	// UserID is the reader whose log the operation belongs to.
	//
	// It is reachable through the device and stored anyway, because the
	// position below is scoped per reader and every pull filters on it (C08).
	UserID uuid.UUID

	// DeviceID is the device that authored the change. RN10 requires it to be
	// the device making the call, which is what stops one device from writing
	// under another's clock entry — and the entry it writes is keyed by this
	// value, so an operation authored under a device nobody registered would
	// introduce a causal history no node can attribute.
	DeviceID uuid.UUID

	// Position is this node's order for this reader's log, zero until the row
	// is written here.
	Position int64

	// Target is the record the change was made to.
	Target Target

	// Kind is what the change did to it.
	Kind Kind

	// Delta is the fields it wrote and nothing else (RN06).
	Delta Delta

	// VectorClock is the causal version the change was authored at, which the
	// reconciler consults before anything else (RN02).
	VectorClock crdt.VectorClock

	// CreatedAt is the instant the authoring device stamped, on the clock C01
	// describes. It is the tie-break for the concurrent case, which is why it
	// has to be a hybrid logical clock and not a wall clock, and why it
	// travels as a distinct message rather than as a timestamp.
	CreatedAt time.Time
}

// Operation is one change to one record (MER: operacao_sync; sync.operations).
type Operation struct {
	// ID is the identifier the authoring device minted. It is what makes
	// receiving idempotent, and this node never mints one of its own.
	ID uuid.UUID

	Props
}

// New records a change a device authored.
//
// It validates what a node must not store rather than what a client should
// have sent. Everything it refuses would make the row unusable to a
// reconciler: an operation nobody can deduplicate, one that belongs to no
// reader, one that names no record, one whose payload says nothing, and one
// whose causal history does not include its own author.
//
// That last check is the one worth stating. A clock that does not count the
// authoring device is a version claiming to have been written by a device that
// had not written anything, which makes the operation concurrent with the very
// write it was derived from — and the tie-break would then settle a pair that
// causality should have ordered.
func New(id uuid.UUID, props *Props) (*Operation, error) {
	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the change could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidOperation).
			WithField(field, reason)
	}

	switch {
	case id == (uuid.UUID{}):
		return nil, invalid("id", "an operation must carry the identifier the device minted for it")
	case props.UserID == (uuid.UUID{}):
		return nil, invalid("user_id", "an operation must name the reader whose log it belongs to")
	case props.DeviceID == (uuid.UUID{}):
		return nil, invalid("device_id", "an operation must name the device that authored it")
	case props.CreatedAt.IsZero():
		return nil, invalid("created_at", "an operation must say when it was authored")
	}

	if err := props.Target.Validate(); err != nil {
		return nil, err
	}

	if err := props.Kind.Validate(); err != nil {
		return nil, err
	}

	if err := props.Delta.Validate(props.Kind); err != nil {
		return nil, err
	}

	if props.VectorClock.Get(crdt.Author(props.DeviceID)) == 0 {
		return nil, invalid("vector_clock",
			"an operation must count itself as an event of the device that authored it")
	}

	stored := *props
	stored.VectorClock = props.VectorClock.Compact()
	stored.CreatedAt = props.CreatedAt.UTC().Truncate(crdt.Resolution)

	// The position is this node's to allocate and never the sender's to claim.
	stored.Position = 0

	return &Operation{ID: id, Props: stored}, nil
}

// Restore rebuilds an operation already stored, without any of the checks
// [New] makes.
//
// The row was validated by the constructor that wrote it, and re-validating
// here would make an operation this node can no longer parse — one naming an
// entity a later version added, replicated back — unreadable rather than
// merely unfamiliar.
func Restore(id uuid.UUID, props *Props) *Operation {
	return &Operation{ID: id, Props: *props}
}

// PlaceAt records the position this node allocated for the operation.
//
// It is the one thing about a stored operation that is not what the author
// said, and it is set once: the log is append-only, and a row whose position
// moved would move every cursor that had already passed it.
func (o *Operation) PlaceAt(position int64) { o.Position = position }

// Author renders the authoring device as the key its vector clock entry is
// counted under.
func (o *Operation) Author() crdt.DeviceID { return crdt.Author(o.DeviceID) }

// Revision is the causal state the change claims for the record it targets.
//
// It is what the reconciler compares against the record's own: the clock
// decides, and when it reports the two versions concurrent the timestamp and
// the device settle it (C01). The tombstone is the kind of change rather than
// a field of it, which is what makes a deletion reconcile like any other
// write.
func (o *Operation) Revision() crdt.Revision {
	return crdt.Revision{
		VectorClock: o.VectorClock,
		UpdatedAt:   o.CreatedAt,
		DeviceID:    o.DeviceID,
		Deleted:     o.Kind == KindDelete,
	}
}

// Version is [Operation.Revision] for a record that has exactly one writer,
// which is reading progress and nothing else (C05).
//
// The two fields it drops are the two that break a tie, and a row whose only
// writer is the device the row names can never have one to break.
func (o *Operation) Version() crdt.Version {
	return crdt.Version{VectorClock: o.VectorClock, UpdatedAt: o.CreatedAt}
}

// IsAuthoredBy reports whether device authored the change, which is what RN10
// checks a pushed batch against.
func (o *Operation) IsAuthoredBy(device uuid.UUID) bool { return o.DeviceID == device }
