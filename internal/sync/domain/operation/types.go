package operation

import (
	"encoding/json"
	"slices"
	"strings"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseTargetEntity = "sync/operation: parse target entity"
	opParseKind         = "sync/operation: parse kind"
	opValidateTarget    = "sync/operation: validate target"
	opValidateDelta     = "sync/operation: validate delta"
)

// The stable machine-readable codes this package attaches to the errors it
// raises.
const (
	// CodeInvalidTargetEntity is a kind of record this node cannot replicate.
	CodeInvalidTargetEntity = "invalid_target_entity"
	// CodeInvalidKind is a kind of change this node cannot name.
	CodeInvalidKind = "invalid_operation_kind"
	// CodeInvalidTarget is a target that names no record.
	CodeInvalidTarget = "invalid_operation_target"
	// CodeInvalidDelta is a payload that is not the changed fields of a
	// record.
	CodeInvalidDelta = "invalid_operation_delta"
)

// TargetEntity is which kind of record an operation changed.
//
// It is named logically rather than by table, because the same name travels in
// three places — this contract, sync.operations.target_entity, and the SQLite
// schema on the device — and a name that meant a table would have to be
// translated at every one of them.
type TargetEntity string

// The kinds of record sync.operations_target_entity admits, which is the
// replicable set of the whole node.
const (
	// TargetEbook is a work in the reader's collection (library.ebooks).
	TargetEbook TargetEntity = "ebook"
	// TargetCollection is a grouping (library.collections).
	TargetCollection TargetEntity = "collection"
	// TargetEbookCollection is the filing of a work under a grouping
	// (library.ebook_collections), which replicates on its own terms (C06).
	TargetEbookCollection TargetEntity = "ebook_collection"
	// TargetReadingProgress is where one device stopped (reading.progress).
	TargetReadingProgress TargetEntity = "reading_progress"
	// TargetAnnotation is a mark the reader left (reading.annotations).
	TargetAnnotation TargetEntity = "annotation"
)

// targetEntities is the set above, in the order the contract enumerates it.
//
// It is a function rather than a package-level slice because the project's own
// linter forbids the second, and because a set that cannot be appended to from
// somewhere else is a set with one definition.
func targetEntities() []TargetEntity {
	return []TargetEntity{
		TargetEbook,
		TargetCollection,
		TargetEbookCollection,
		TargetReadingProgress,
		TargetAnnotation,
	}
}

// String renders the kind of record.
func (t TargetEntity) String() string { return string(t) }

// Validate reports why the kind of record is not usable, or nil.
//
// The set is closed here as it is in the column, and for the reason the schema
// gives: a typo in an entity name would otherwise produce operations no
// reconciler ever applies, and nothing would say so. An operation naming a kind
// this node does not know is refused at the edge, which is what turns a peer
// running a later version into a reported rejection rather than a silence.
func (t TargetEntity) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the kind of record is not usable").
			WithOp(opParseTargetEntity).
			WithCode(CodeInvalidTargetEntity).
			WithField("target_entity", reason)
	}

	if t == "" {
		return invalid("an operation must say which kind of record it changed")
	}

	if !slices.Contains(targetEntities(), t) {
		return invalid("this node replicates ebook, collection, ebook_collection, " +
			"reading_progress and annotation")
	}

	return nil
}

// ParseTargetEntity lowercases s and validates the result.
func ParseTargetEntity(s string) (TargetEntity, error) {
	entity := TargetEntity(strings.ToLower(strings.TrimSpace(s)))
	if err := entity.Validate(); err != nil {
		return "", err
	}

	return entity, nil
}

// Kind is what the operation did to the record it names.
//
// Deletion is one of the three and not a separate mechanism: it sets the
// tombstone, travels as a delta like any other write, and reconciles by the
// same rule. A record removed outright would be resurrected by the next node
// that had not yet heard about the removal.
type Kind string

// The kinds sync.operations_kind admits.
const (
	// KindInsert records a record that did not exist here before.
	KindInsert Kind = "insert"
	// KindUpdate records a change to the fields the delta names.
	KindUpdate Kind = "update"
	// KindDelete records the tombstone.
	KindDelete Kind = "delete"
)

// kinds is the set above, in the order the contract enumerates it.
func kinds() []Kind { return []Kind{KindInsert, KindUpdate, KindDelete} }

// String renders the kind of change.
func (k Kind) String() string { return string(k) }

// Validate reports why the kind of change is not usable, or nil.
func (k Kind) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the kind of change is not usable").
			WithOp(opParseKind).
			WithCode(CodeInvalidKind).
			WithField("operation", reason)
	}

	if k == "" {
		return invalid("an operation must say what it did")
	}

	if !slices.Contains(kinds(), k) {
		return invalid("this node knows insert, update and delete")
	}

	return nil
}

// ParseKind lowercases s and validates the result.
func ParseKind(s string) (Kind, error) {
	kind := Kind(strings.ToLower(strings.TrimSpace(s)))
	if err := kind.Validate(); err != nil {
		return "", err
	}

	return kind, nil
}

// Target is the record an operation changed.
//
// The pair travels together because neither half means anything alone: the
// identifier is a uuid in five different tables, and which of them it is in is
// the entity beside it. It is also what sync.operations_target_idx is on, and
// what the reconciler resolves the record through.
type Target struct {
	// Entity is which kind of record it is.
	Entity TargetEntity
	// ID is the record's own identifier, minted by whoever created it and the
	// same value on every node that holds the record.
	ID uuid.UUID
}

// Validate reports why the target is not usable, or nil.
func (t Target) Validate() error {
	if err := t.Entity.Validate(); err != nil {
		return err
	}

	if t.ID == (uuid.UUID{}) {
		return errs.New(errs.KindInvalidArgument, "the target of the change is not usable").
			WithOp(opValidateTarget).
			WithCode(CodeInvalidTarget).
			WithField("target_id", "an operation must name the record it changed")
	}

	return nil
}

// String renders the target as the pair it is, for a log line or a failing
// test.
func (t Target) String() string { return t.Entity.String() + ":" + t.ID.String() }

// Delta is the fields the operation changed and nothing else (RN06).
//
// The keys are the field names of the record, in the vocabulary of the
// contract, and the values are left encoded on purpose: what a value means
// depends on the entity it belongs to, and this package owns none of them. The
// adapter that holds the record is what decodes them, which is also what stops
// a field name this node does not know from being a parse failure here rather
// than a rejection there.
//
// It is an object and never a whole record, and that is the whole of why
// reconciliation can be per field at all: a delta naming two fields claims
// those two and leaves the rest to whichever device wrote them last.
type Delta map[string]json.RawMessage

// Fields returns the field names the delta claims, sorted, so that a log line
// or a failing test reads the same way twice.
func (d Delta) Fields() []string {
	fields := make([]string, 0, len(d))
	for field := range d {
		fields = append(fields, field)
	}

	slices.Sort(fields)

	return fields
}

// Claims reports whether the delta writes field.
func (d Delta) Claims(field string) bool {
	_, ok := d[field]

	return ok
}

// IsEmpty reports whether the delta claims nothing.
func (d Delta) IsEmpty() bool { return len(d) == 0 }

// Validate reports why the delta is not usable for kind, or nil.
//
// An insert and an update must claim something: a write that names no field is
// a version of the record that says nothing, and it would still take the
// tie-break away from the write that did say something. A deletion claims
// nothing by construction — what it changes is the tombstone, which is
// replication metadata and not a field of the record — and a delta sent with
// one is kept rather than refused, because a device that also states the
// fields it was holding costs this node nothing.
func (d Delta) Validate(kind Kind) error {
	if kind != KindDelete && d.IsEmpty() {
		return errs.New(errs.KindInvalidArgument, "the change names no field").
			WithOp(opValidateDelta).
			WithCode(CodeInvalidDelta).
			WithField("delta", "an insert or an update must say which fields it wrote")
	}

	return nil
}

// MarshalJSON encodes the delta as the object the column holds.
//
// An empty delta is rendered as {} and never as null, because
// sync.operations_delta_is_object refuses anything that is not an object and a
// deletion legitimately claims no field. encoding/json sorts the keys, so the
// same delta always produces the same bytes.
func (d Delta) MarshalJSON() ([]byte, error) {
	fields := map[string]json.RawMessage(d)
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}

	// The conversion drops the method set, which is what stops this from
	// recursing into itself.
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalidArgument, "the change could not be encoded").
			WithOp(opValidateDelta).
			WithCode(CodeInvalidDelta)
	}

	return encoded, nil
}

// UnmarshalJSON decodes a delta. A JSON null decodes to a delta that claims
// nothing, which is the same value an empty object decodes to.
func (d *Delta) UnmarshalJSON(data []byte) error {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		return errs.Wrap(err, errs.KindInvalidArgument, "the change could not be decoded").
			WithOp(opValidateDelta).
			WithCode(CodeInvalidDelta)
	}

	*d = decoded

	return nil
}
