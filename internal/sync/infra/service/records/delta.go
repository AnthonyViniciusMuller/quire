package records

import (
	"encoding/json"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// The field names a delta claims. They are the schema's and the contract's,
// spelled once here so that the five reconcilers cannot spell them two ways.
const (
	fieldTitle        = "title"
	fieldAuthor       = "author"
	fieldPublisher    = "publisher"
	fieldLanguage     = "language"
	fieldExtra        = "extra_metadata"
	fieldFormat       = "format"
	fieldContentHash  = "content_hash"
	fieldSizeBytes    = "size_bytes"
	fieldImportedAt   = "imported_at"
	fieldName         = "name"
	fieldKind         = "kind"
	fieldDescription  = "description"
	fieldCreatedAt    = "created_at"
	fieldEbookID      = "ebook_id"
	fieldCollectionID = "collection_id"
	fieldText         = "text"
	fieldLocator      = "locator"
	fieldPercent      = "percent"
)

// opDecode is the operation reported by this file, in the form the errs
// package expects.
const opDecode = "sync/records: decode"

// claim reads the value the delta claims for field into target, and reports
// whether the delta claimed it at all.
//
// The two answers are different and both are needed. A field the delta does
// not name is one the change did not write, and it keeps whatever the record
// already held — which is the whole of what a delta being the changed fields
// and nothing else means (RN06). A field the delta names as null is a field
// the change cleared, which is why the optional ones are decoded into a
// pointer.
func claim[T any](delta operation.Delta, field string, target *T) (bool, error) {
	raw, claimed := delta[field]
	if !claimed {
		return false, nil
	}

	if err := json.Unmarshal(raw, target); err != nil {
		return false, errs.Wrap(err, errs.KindInvalidArgument, "the change claims a field it cannot fill").
			WithOp(opDecode).
			WithCode(operation.CodeInvalidDelta).
			WithField(field, "the value is not of the kind this field holds")
	}

	return true, nil
}

// required reads a field the change cannot be applied without.
//
// It is what an insert uses: a record created out of a delta that named none
// of the columns the row requires would be refused by the constraint rather
// than by the reconciler, and the caller would be told about a table.
func required[T any](delta operation.Delta, field string, target *T) error {
	claimed, err := claim(delta, field, target)
	if err != nil {
		return err
	}

	if !claimed {
		return errs.New(errs.KindInvalidArgument, "the change leaves out a field the record needs").
			WithOp(opDecode).
			WithCode(operation.CodeInvalidDelta).
			WithField(field, "a record cannot be created without it")
	}

	return nil
}

// value returns what a pointer holds, and the zero value for the absence a
// null in the delta decoded to.
func value[T any](pointer *T) T {
	if pointer == nil {
		var zero T

		return zero
	}

	return *pointer
}

// text applies a claimed string field through the parser that owns its value
// object, and leaves the target as it was when the delta does not claim it.
//
// The parser is what makes a value the column would refuse a rejection with a
// field name on it rather than a constraint violation with a table name on it.
// A field claimed as null parses the empty string, which is how absence is
// spelled in every one of these types — it is what a reader removing an author
// they had corrected is asking for.
func text[T any](delta operation.Delta, field string, parse func(string) (T, error), target *T) error {
	var claimed *string

	written, err := claim(delta, field, &claimed)
	if err != nil || !written {
		return err
	}

	parsed, err := parse(value(claimed))
	if err != nil {
		return err
	}

	*target = parsed

	return nil
}

// assign applies a claimed field that needs no parsing, decoding it straight
// into the value the record holds.
func assign[T any](delta operation.Delta, field string, target *T) error {
	var claimed T

	written, err := claim(delta, field, &claimed)
	if err != nil || !written {
		return err
	}

	*target = claimed

	return nil
}
