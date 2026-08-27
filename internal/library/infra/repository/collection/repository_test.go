package collection

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, reader, phone := uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	description := "o que a faculdade pediu"

	grouping := toDomain(&librarydb.LibraryCollection{
		ID:          id,
		UserID:      reader,
		Name:        "Literatura brasileira",
		Kind:        "category",
		Description: &description,
		CreatedAt:   at,
		VectorClock: crdt.VectorClock{crdt.Author(phone): 1},
		UpdatedAt:   at,
		DeviceID:    &phone,
	})

	switch {
	case grouping.ID != id:
		t.Error("the row was rebuilt under a new identifier, so every filing under it would be orphaned")
	case !grouping.BelongsTo(reader):
		t.Error("the grouping no longer names the reader who defined it")
	case grouping.Name != "Literatura brasileira" || grouping.Kind != collection.KindCategory:
		t.Errorf("the description was not carried across: %+v", grouping.Details)
	case grouping.Description != "o que a faculdade pediu":
		t.Error("what the reader wrote about the grouping was lost")
	case grouping.Revision.DeviceID != phone:
		t.Error("the revision lost the device whose write the row reflects")
	}
}

func TestToDomainOfAGroupingWithNoDescription(t *testing.T) {
	t.Parallel()

	grouping := toDomain(&librarydb.LibraryCollection{
		ID:      uuid.New(),
		UserID:  uuid.New(),
		Name:    "later",
		Kind:    "collection",
		Deleted: true,
	})

	if !grouping.Description.IsZero() {
		t.Error("an absent description came back as something the reader wrote")
	}

	if !grouping.IsDeleted() {
		t.Error("a tombstone came back as a grouping the reader still has")
	}
}

func TestOptionalStringRendersAbsenceAsNull(t *testing.T) {
	t.Parallel()

	if optionalString("") != nil {
		t.Error("an absent description was stored as one the reader wrote")
	}

	if value := optionalString("later"); value == nil || *value != "later" {
		t.Error("a present description was dropped")
	}
}
