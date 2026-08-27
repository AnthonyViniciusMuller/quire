package collection_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// created is the instant the groupings below were defined at.
var created = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// details is a grouping with every field a reader can write.
func details() *collection.Details {
	return &collection.Details{
		Name:        "Literatura brasileira",
		Kind:        collection.KindCollection,
		Description: "o que a faculdade pediu e o que ficou",
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	reader, phone := uuid.New(), uuid.New()

	grouping, err := collection.New(reader, details(), phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case grouping.ID == (uuid.UUID{}):
		t.Error("the grouping was recorded without an identifier")
	case !grouping.BelongsTo(reader):
		t.Error("the grouping does not name the reader who defined it")
	case grouping.Kind != collection.KindCollection:
		t.Errorf("the grouping is a %q", grouping.Kind)
	case grouping.IsDeleted():
		t.Error("a grouping that was just defined is a tombstone")
	case grouping.Revision.DeviceID != phone:
		t.Error("the revision does not name the device that defined the grouping")
	}
}

func TestNewRefusesAGroupingNobodyCanBeAttributedTo(t *testing.T) {
	t.Parallel()

	reader, phone := uuid.New(), uuid.New()

	cases := map[string]struct {
		userID, device uuid.UUID
		at             time.Time
		field          string
	}{
		"no reader": {uuid.UUID{}, phone, created, "user_id"},
		"no device": {reader, uuid.UUID{}, created, "device_id"},
		"no time":   {reader, phone, time.Time{}, "created_at"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := collection.New(testCase.userID, details(), testCase.device, testCase.at)
			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Fatalf("New = %v, want an invalid argument", err)
			}

			if fields := errs.FieldsOf(err); len(fields) != 1 || fields[0].Name != testCase.field {
				t.Errorf("the refusal points at %v, want %s", fields, testCase.field)
			}
		})
	}
}

// A client that says nothing is making a shelf, which is the default the
// column carries.
func TestParseKindDefaultsToACollection(t *testing.T) {
	t.Parallel()

	kind, err := collection.ParseKind("")
	if err != nil {
		t.Fatalf("ParseKind: %v", err)
	}

	if kind != collection.KindCollection {
		t.Errorf("an unstated kind became %q, want a collection", kind)
	}

	if _, err := collection.ParseKind("shelf"); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("ParseKind(\"shelf\") = %v, want an invalid argument", err)
	}
}

func TestEditStampsTheDeviceThatMadeIt(t *testing.T) {
	t.Parallel()

	phone, tablet := uuid.New(), uuid.New()

	grouping, err := collection.New(uuid.New(), details(), phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := grouping.Revision

	renamed := details()
	renamed.Name = "Literatura"

	if err := grouping.Edit(renamed, tablet, created.Add(time.Hour)); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	switch {
	case grouping.Name != "Literatura":
		t.Error("the rename was not applied")
	case grouping.Revision.DeviceID != tablet:
		t.Error("the revision names the device that defined the grouping rather than the one that edited it")
	case !before.VectorClock.HappensBefore(grouping.Revision.VectorClock):
		t.Error("the edit does not causally dominate the version it derives from")
	}
}

func TestDeleteTombstonesRatherThanRemoving(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	grouping, err := collection.New(uuid.New(), details(), phone, created)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := grouping.Revision
	grouping.Delete(phone, created.Add(time.Hour))

	switch {
	case !grouping.IsDeleted():
		t.Error("the grouping was not marked removed")
	case !before.VectorClock.HappensBefore(grouping.Revision.VectorClock):
		t.Error("the deletion does not causally dominate the version it removed")
	}
}
