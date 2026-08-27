package ebook

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, reader, phone := uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	author, publisher, language := "Euclides da Cunha", "Laemmert", "pt-BR"
	size := int64(1024)

	work, err := toDomain(&librarydb.LibraryEbook{
		ID:            id,
		UserID:        reader,
		Title:         "Os Sertões",
		Author:        &author,
		Publisher:     &publisher,
		Language:      &language,
		Format:        "epub",
		ContentHash:   "abc",
		SizeBytes:     &size,
		ExtraMetadata: []byte(`{"isbn":"9788535911190"}`),
		ImportedAt:    at,
		VectorClock:   crdt.VectorClock{crdt.Author(phone): 1},
		UpdatedAt:     at,
		DeviceID:      &phone,
	})
	if err != nil {
		t.Fatalf("toDomain: %v", err)
	}

	switch {
	case work.ID != id:
		t.Error("the row was rebuilt under a new identifier, so every annotation on it would be orphaned")
	case !work.BelongsTo(reader):
		t.Error("the work no longer names the reader whose collection it is in")
	case work.Author != "Euclides da Cunha" || work.Publisher != "Laemmert" || work.Language != "pt-BR":
		t.Errorf("the description was not carried across: %+v", work.Details)
	case work.Size != 1024 || work.Format != ebook.FormatEPUB:
		t.Errorf("the file was not carried across: %+v", work.File)
	case work.Extra["isbn"] != "9788535911190":
		t.Errorf("the metadata RF05 exists for was lost: %v", work.Extra)
	case work.Revision.DeviceID != phone:
		t.Error("the revision lost the device whose write the row reflects, so the tie-break has one half")
	case work.IsDeleted():
		t.Error("a row that is present came back as a tombstone")
	}
}

// Every optional column absent at once is the ordinary case for a file whose
// container carried no metadata, not an edge case.
func TestToDomainOfAWorkTheFileSaidNothingAbout(t *testing.T) {
	t.Parallel()

	work, err := toDomain(&librarydb.LibraryEbook{
		ID:      uuid.New(),
		UserID:  uuid.New(),
		Title:   "untitled.epub",
		Format:  "epub",
		Deleted: true,
	})
	if err != nil {
		t.Fatalf("toDomain: %v", err)
	}

	switch {
	case !work.Author.IsZero() || !work.Publisher.IsZero() || !work.Language.IsZero():
		t.Error("an absent field came back as something the file claimed")
	case !work.Size.IsZero():
		t.Error("an absent length came back as a claim about the file")
	case !work.Extra.IsZero():
		t.Error("absent metadata came back as an empty object, which is a different claim")
	case work.Revision.DeviceID != (uuid.UUID{}):
		t.Error("a NULL device came back as one")
	case !work.IsDeleted():
		t.Error("a tombstone came back as a work the reader still has")
	}
}

// The values were validated by the constructor that wrote them, so the row is
// cast and not re-parsed: a format a later version added and replicated back
// has to stay readable rather than become unreadable.
func TestToDomainDoesNotRevalidateWhatTheRowHolds(t *testing.T) {
	t.Parallel()

	work, err := toDomain(&librarydb.LibraryEbook{
		ID:     uuid.New(),
		UserID: uuid.New(),
		Title:  "a work from a later version",
		Format: "azw3",
	})
	if err != nil {
		t.Fatalf("toDomain refused a row this node did not write: %v", err)
	}

	if work.Format != "azw3" {
		t.Errorf("the format came back as %q", work.Format)
	}
}

func TestMarshalMetadataDistinguishesAbsenceFromEmptiness(t *testing.T) {
	t.Parallel()

	encoded, err := marshalMetadata(nil, opCreate)
	if err != nil {
		t.Fatalf("marshalMetadata: %v", err)
	}

	if encoded != nil {
		t.Errorf("absent metadata was stored as %q, want the NULL the column holds", encoded)
	}

	encoded, err = marshalMetadata(ebook.Metadata{"series": "Sertões"}, opCreate)
	if err != nil {
		t.Fatalf("marshalMetadata: %v", err)
	}

	if string(encoded) != `{"series":"Sertões"}` {
		t.Errorf("the metadata was stored as %q", encoded)
	}
}

func TestOptionalValuesRenderAbsenceAsNull(t *testing.T) {
	t.Parallel()

	if optionalString("") != nil || optionalInt64(0) != nil {
		t.Error("an absent value was stored as something the file claimed")
	}

	if value := optionalString("epub"); value == nil || *value != "epub" {
		t.Error("a present string was dropped")
	}

	if value := optionalInt64(1); value == nil || *value != 1 {
		t.Error("a present length was dropped")
	}
}
