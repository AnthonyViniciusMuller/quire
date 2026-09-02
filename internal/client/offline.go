package client

import (
	"encoding/json"
	"uuid"

	"google.golang.org/protobuf/types/known/structpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/crdtpb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The kinds of record a change can address, in the contract's vocabulary. They
// are the names sync.operations.target_entity admits, and the node reads them
// in internal/sync/domain/operation.
const (
	entityEbook      = "ebook"
	entityCollection = "collection"
	entityFiling     = "ebook_collection"
	entityPosition   = "reading_progress"
	entityAnnotation = "annotation"
)

// What a change did to the record it names.
const (
	kindInsert = "insert"
	kindUpdate = "update"
	kindDelete = "delete"
)

// The field names a delta claims.
//
// They are the contract's names for the fields of a record — the same ones a
// field mask carries on the connected path — and they are spelled here because
// this is the other end of the protocol: the node reads them in
// internal/sync/infra/service/records, and a device that spelled one
// differently would be claiming a field nobody has.
const (
	fieldTitle        = "title"
	fieldAuthor       = "author"
	fieldPublisher    = "publisher"
	fieldLanguage     = "language"
	fieldExtra        = "extra_metadata"
	fieldFormat       = "format"
	fieldContentHash  = "content_hash"
	fieldSizeBytes    = "size_bytes"
	fieldName         = "name"
	fieldKind         = "kind"
	fieldDescription  = "description"
	fieldEbookID      = "ebook_id"
	fieldCollectionID = "collection_id"
	fieldText         = "text"
	fieldLocator      = "locator"
	fieldPercent      = "percent"
)

// Written is what a change reports: the record it touched, and which path it
// took.
//
// A caller does not branch on Queued. It is there to be shown to the reader,
// because the two paths differ in when the change reaches the node and in
// nothing else: a queued change is stamped with this device's clock and is
// handed over by the next push, and the node applies it by the same rule it
// applies one that arrived over the connected path. It is set by a client
// opened offline and by one that found the node out of reach alike.
type Written struct {
	// Target is the record the change addressed, whether it was minted here or
	// named by the caller.
	Target uuid.UUID

	// Queued says the change went into the local log rather than to the node.
	Queued bool

	// ContentMissing is the node's answer to whether it already holds the bytes
	// of the work that was just registered. It is meaningful only on a change
	// that reached the node: a queued one has not been answered yet, and the
	// upload that follows is what asks.
	ContentMissing bool
}

// delta is the changed fields of a record and nothing else (RN06).
type delta map[string]json.RawMessage

// set claims a field.
//
// A nil value is claimed as JSON null, which is a field the change cleared —
// not a field it left alone. The difference is the whole of what per-field
// reconciliation rests on: a field the delta does not name keeps whatever
// device wrote it last.
func (d delta) set(field string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errs.Wrap(err, errs.KindInvalidArgument, "the change could not be encoded").
			WithOp(opClient).
			WithField(field, "the value is not of the kind this field holds")
	}

	d[field] = encoded

	return nil
}

// setText claims a string field, or clears it when the value is empty.
//
// Every optional string in this contract spells absence as the empty string, so
// a reader removing an author they had corrected and a reader who never gave
// one produce the same delta — which is what the node's own parsers expect.
func (d delta) setText(field, value string) error {
	if value == "" {
		return d.set(field, nil)
	}

	return d.set(field, value)
}

// author appends a change to the local log, stamped on this device's clock.
//
// The three stamps are what make the change reconcilable, and each answers a
// different question. The vector clock ticks over the version this device last
// saw of the record, which is what says whether the change causally follows
// what somebody else wrote. The instant comes from this device's hybrid logical
// clock, which is C01's rule applied on the device rather than on the node. And
// the identifier is minted here and never again, so the same change arriving at
// a node twice by two routes is recognized rather than applied twice.
func (c *Client) author(entity, key string, target uuid.UUID, kind string, changed delta) (Written, error) {
	device, err := c.requireDevice()
	if err != nil {
		return Written{}, err
	}

	known := c.state.Records[key]
	clock := known.Clock.Tick(crdt.Author(device))
	at := c.clock.Now()

	if changed == nil {
		changed = delta{}
	}

	c.state.Pending = append(c.state.Pending, Operation{
		ID:          uuid.New(),
		DeviceID:    device,
		Entity:      entity,
		TargetID:    target,
		Kind:        kind,
		Delta:       changed,
		VectorClock: clock,
		CreatedAt:   at,
	})

	c.state.Records[key] = Record{ID: target, Clock: clock}

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: target, Queued: true}, nil
}

// target returns the identifier a change to a record addressed by its natural
// key must carry, minting one the first time this device writes the record.
//
// The filing of a work under a grouping and a reading position are the two, and
// C18 is why: the row carries a surrogate key each replica mints for itself, so
// what identifies the record across the federation is the pair in the delta.
// The node resolves it by that pair; this identifier is only what this device
// calls the row, and reusing it is what keeps one device from filling the log
// with a new name for the same record on every write.
func (c *Client) target(key string) uuid.UUID {
	if known, ok := c.state.Records[key]; ok && known.ID != (uuid.UUID{}) {
		return known.ID
	}

	return uuid.New()
}

// message renders a queued change as the contract carries it.
func (o *Operation) message() (*quirev1.Operation, error) {
	claimed, err := json.Marshal(o.Delta)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "a queued change could not be rendered").
			WithOp(opClient)
	}

	rendered := &structpb.Struct{}
	if err = rendered.UnmarshalJSON(claimed); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "a queued change could not be rendered").
			WithOp(opClient)
	}

	return &quirev1.Operation{
		Id:           o.ID.String(),
		DeviceId:     o.DeviceID.String(),
		TargetEntity: targetEntity(o.Entity),
		TargetId:     o.TargetID.String(),
		Operation:    operationKind(o.Kind),
		Delta:        rendered,
		VectorClock:  crdtpb.VectorClock(o.VectorClock),
		CreatedAt:    crdtpb.Timestamp(o.CreatedAt),
	}, nil
}

// targetEntity renders the kind of record as the enumerator the contract names
// it by.
func targetEntity(entity string) quirev1.TargetEntity {
	switch entity {
	case entityEbook:
		return quirev1.TargetEntity_TARGET_ENTITY_EBOOK
	case entityCollection:
		return quirev1.TargetEntity_TARGET_ENTITY_COLLECTION
	case entityFiling:
		return quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION
	case entityPosition:
		return quirev1.TargetEntity_TARGET_ENTITY_READING_PROGRESS
	case entityAnnotation:
		return quirev1.TargetEntity_TARGET_ENTITY_ANNOTATION
	default:
		return quirev1.TargetEntity_TARGET_ENTITY_UNSPECIFIED
	}
}

// entityName reads an enumerator back into the name this client keys its
// records by.
func entityName(entity quirev1.TargetEntity) string {
	switch entity {
	case quirev1.TargetEntity_TARGET_ENTITY_EBOOK:
		return entityEbook
	case quirev1.TargetEntity_TARGET_ENTITY_COLLECTION:
		return entityCollection
	case quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION:
		return entityFiling
	case quirev1.TargetEntity_TARGET_ENTITY_READING_PROGRESS:
		return entityPosition
	case quirev1.TargetEntity_TARGET_ENTITY_ANNOTATION:
		return entityAnnotation
	case quirev1.TargetEntity_TARGET_ENTITY_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// operationKind renders what the change did.
func operationKind(kind string) quirev1.OperationKind {
	switch kind {
	case kindInsert:
		return quirev1.OperationKind_OPERATION_KIND_INSERT
	case kindUpdate:
		return quirev1.OperationKind_OPERATION_KIND_UPDATE
	case kindDelete:
		return quirev1.OperationKind_OPERATION_KIND_DELETE
	default:
		return quirev1.OperationKind_OPERATION_KIND_UNSPECIFIED
	}
}

// Pending is what this device has authored and not yet handed over, in the
// order it authored it.
func (c *Client) Pending() []Operation { return c.state.Pending }
