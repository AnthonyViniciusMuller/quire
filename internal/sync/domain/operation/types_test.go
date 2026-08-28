package operation_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

func TestParseTargetEntity(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ebook", "collection", "ebook_collection", "reading_progress", "annotation"} {
		entity, err := operation.ParseTargetEntity(" " + name + " ")
		if err != nil {
			t.Fatalf("ParseTargetEntity(%q): %v", name, err)
		}

		if entity.String() != name {
			t.Errorf("ParseTargetEntity(%q) = %q", name, entity)
		}
	}
}

// The set is the one sync.operations_target_entity admits. A name outside it
// would produce operations no reconciler ever applies, which is exactly what
// the constraint exists to stop, so the entity refuses it at the edge instead
// of leaving the column to.
func TestParseTargetEntityRefusesWhatTheColumnWould(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "  ", "ebooks", "library.ebooks", "shelf"} {
		if _, err := operation.ParseTargetEntity(name); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("ParseTargetEntity(%q) = %v, want an invalid argument", name, err)
		}
	}
}

func TestParseKind(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"insert", "update", "delete"} {
		kind, err := operation.ParseKind(name)
		if err != nil {
			t.Fatalf("ParseKind(%q): %v", name, err)
		}

		if kind.String() != name {
			t.Errorf("ParseKind(%q) = %q", name, kind)
		}
	}

	for _, name := range []string{"", "upsert", "drop"} {
		if _, err := operation.ParseKind(name); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("ParseKind(%q) = %v, want an invalid argument", name, err)
		}
	}
}

func TestTargetValidate(t *testing.T) {
	t.Parallel()

	if err := (operation.Target{Entity: operation.TargetEbook, ID: uuid.New()}).Validate(); err != nil {
		t.Fatalf("a well-formed target was refused: %v", err)
	}

	// Neither half means anything alone: the identifier is a uuid in five
	// tables, and which of them it is in is the entity beside it.
	for name, target := range map[string]operation.Target{
		"no entity": {ID: uuid.New()},
		"no record": {Entity: operation.TargetAnnotation},
	} {
		if err := target.Validate(); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("%s: Validate = %v, want an invalid argument", name, err)
		}
	}
}

// An insert or an update that names no field is a version of the record that
// says nothing, and it would still take the tie-break away from the write that
// did say something. A deletion claims nothing by construction.
func TestDeltaValidate(t *testing.T) {
	t.Parallel()

	claiming := operation.Delta{"title": json.RawMessage(`"Vidas Secas"`)}

	for _, kind := range []operation.Kind{operation.KindInsert, operation.KindUpdate} {
		if err := claiming.Validate(kind); err != nil {
			t.Errorf("a delta naming a field was refused for %s: %v", kind, err)
		}

		if err := (operation.Delta{}).Validate(kind); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("an empty delta was accepted for %s: %v", kind, err)
		}
	}

	if err := operation.Delta(nil).Validate(operation.KindDelete); err != nil {
		t.Errorf("a deletion that claims no field was refused: %v", err)
	}
}

func TestDeltaFieldsAreSorted(t *testing.T) {
	t.Parallel()

	delta := operation.Delta{
		"title":  json.RawMessage(`"Vidas Secas"`),
		"author": json.RawMessage(`"Graciliano Ramos"`),
	}

	if fields := delta.Fields(); !slices.Equal(fields, []string{"author", "title"}) {
		t.Errorf("Fields = %v, want them sorted", fields)
	}

	if !delta.Claims("title") || delta.Claims("publisher") {
		t.Error("Claims does not report the fields the delta writes")
	}
}

// sync.operations_delta_is_object refuses anything that is not an object, and
// a deletion legitimately claims no field — so an empty delta has to reach the
// column as {} and never as null.
func TestDeltaEncodesAnEmptyClaimAsAnObject(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(operation.Delta(nil))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(encoded) != "{}" {
		t.Errorf("an empty delta encoded as %s, want {}", encoded)
	}

	var decoded operation.Delta
	if err = json.Unmarshal([]byte("null"), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !decoded.IsEmpty() {
		t.Errorf("a null decoded to %v, want a delta that claims nothing", decoded)
	}
}

func TestDeltaRoundTripsThroughTheColumn(t *testing.T) {
	t.Parallel()

	delta := operation.Delta{
		"title":   json.RawMessage(`"Vidas Secas"`),
		"deleted": json.RawMessage(`true`),
	}

	encoded, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded operation.Delta
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !slices.Equal(decoded.Fields(), delta.Fields()) {
		t.Fatalf("the delta came back claiming %v", decoded.Fields())
	}

	for _, field := range delta.Fields() {
		if string(decoded[field]) != string(delta[field]) {
			t.Errorf("%s came back as %s, want %s", field, decoded[field], delta[field])
		}
	}
}
