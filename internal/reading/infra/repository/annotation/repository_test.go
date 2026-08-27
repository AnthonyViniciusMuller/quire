package annotation

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/persist/readingdb"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, work, phone := uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	text := "a sertão é uma sociedade"

	mark := toDomain(&readingdb.ReadingAnnotation{
		ID:          id,
		EbookID:     work,
		DeviceID:    &phone,
		Kind:        "note",
		Text:        &text,
		Locator:     "epubcfi(/6/14!/4/10/3:10)",
		VectorClock: crdt.VectorClock{crdt.Author(phone): 2},
		UpdatedAt:   at,
	})

	switch {
	case mark.ID != id:
		t.Error("the row was rebuilt under a new identifier, so the client's own handle would be wrong")
	case !mark.IsIn(work):
		t.Error("the mark no longer names the work it is in, which is what its reader is established through")
	case mark.Kind != annotation.KindNote || mark.Text.String() != text:
		t.Errorf("the mark was not carried across: %+v", mark.Mark)
	case mark.Locator.String() != "epubcfi(/6/14!/4/10/3:10)":
		t.Errorf("the passage came back as %q", mark.Locator)
	case mark.Revision.DeviceID != phone:
		t.Error("the revision lost the device whose write the row reflects, so the tie-break has one half")
	case mark.IsDeleted():
		t.Error("a row that is present came back as a tombstone")
	}
}

// A highlight the reader left uncommented, on a row whose device the deletion
// of its reader set to NULL. Both absences are ordinary.
func TestToDomainOfAMarkWithNothingWrittenOnIt(t *testing.T) {
	t.Parallel()

	mark := toDomain(&readingdb.ReadingAnnotation{
		ID:      uuid.New(),
		EbookID: uuid.New(),
		Kind:    "highlight",
		Locator: "page=42",
		Deleted: true,
	})

	switch {
	case !mark.Text.IsZero():
		t.Error("an absent text came back as something the reader wrote")
	case mark.Revision.DeviceID != (uuid.UUID{}):
		t.Error("a NULL device came back as one")
	case !mark.IsDeleted():
		t.Error("a tombstone came back as a mark the reader still has")
	}
}

// The kind was validated by the constructor that wrote it, so the row is cast
// and not re-parsed: a kind a later version added and replicated back has to
// stay readable rather than become unreadable.
func TestToDomainDoesNotRevalidateWhatTheRowHolds(t *testing.T) {
	t.Parallel()

	mark := toDomain(&readingdb.ReadingAnnotation{
		ID:      uuid.New(),
		EbookID: uuid.New(),
		Kind:    "underline",
		Locator: "page=42",
	})

	if mark.Kind != "underline" {
		t.Errorf("the kind came back as %q", mark.Kind)
	}
}

func TestOptionalStringRendersAbsenceAsNull(t *testing.T) {
	t.Parallel()

	if optionalString("") != nil {
		t.Error("a mark with nothing written on it was stored as one that says the empty string")
	}

	if value := optionalString("uma nota"); value == nil || *value != "uma nota" {
		t.Error("a present text was dropped")
	}
}
