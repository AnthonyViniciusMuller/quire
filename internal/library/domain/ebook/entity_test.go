package ebook_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// imported is the instant the works below entered the collection.
var imported = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// details is a description with every field a file can carry.
func details() *ebook.Details {
	return &ebook.Details{
		Title:     "Os Sertões",
		Author:    "Euclides da Cunha",
		Publisher: "Laemmert",
		Language:  "pt-BR",
		Extra:     ebook.Metadata{"isbn": "9788535911190"},
	}
}

// file is the bytes those details describe.
func file() *ebook.File {
	return &ebook.File{Format: ebook.FormatEPUB, Hash: ebook.ContentHash(longHex('a')), Size: 1024}
}

func TestNew(t *testing.T) {
	t.Parallel()

	reader, phone := uuid.New(), uuid.New()

	work, err := ebook.New(reader, details(), file(), phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case work.ID == (uuid.UUID{}):
		t.Error("the work was recorded without an identifier")
	case !work.BelongsTo(reader):
		t.Error("the work does not name the reader whose collection it is in")
	case work.Title != "Os Sertões":
		t.Errorf("the work is titled %q", work.Title)
	case work.IsDeleted():
		t.Error("a work that was just imported is a tombstone")
	case work.Revision.DeviceID != phone:
		t.Error("the revision does not name the device that imported the work")
	case work.Revision.VectorClock.Len() != 1:
		t.Error("the causal history of a new work names more than the device that made it")
	case !work.ImportedAt.Equal(imported):
		t.Errorf("the work entered the collection at %s, want %s", work.ImportedAt, imported)
	}
}

func TestNewRefusesAnImportNobodyCanBeAttributedTo(t *testing.T) {
	t.Parallel()

	reader, phone := uuid.New(), uuid.New()

	cases := map[string]struct {
		userID, device uuid.UUID
		at             time.Time
		field          string
	}{
		"no reader": {uuid.UUID{}, phone, imported, "user_id"},
		"no device": {reader, uuid.UUID{}, imported, "device_id"},
		"no time":   {reader, phone, time.Time{}, "imported_at"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ebook.New(testCase.userID, details(), file(), testCase.device, testCase.at)
			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Fatalf("New = %v, want an invalid argument", err)
			}

			if fields := errs.FieldsOf(err); len(fields) != 1 || fields[0].Name != testCase.field {
				t.Errorf("the refusal points at %v, want %s", fields, testCase.field)
			}
		})
	}
}

func TestNewValidatesTheDescriptionAndTheFile(t *testing.T) {
	t.Parallel()

	reader, phone := uuid.New(), uuid.New()

	untitled := details()
	untitled.Title = ""

	if _, err := ebook.New(reader, untitled, file(), phone, imported); errs.CodeOf(err) != ebook.CodeInvalidTitle {
		t.Errorf("an untitled work was refused as %q, want %q", errs.CodeOf(err), ebook.CodeInvalidTitle)
	}

	unhashed := file()
	unhashed.Hash = "not a digest"

	if _, err := ebook.New(reader, details(), unhashed, phone, imported); errs.CodeOf(err) != ebook.CodeInvalidContentHash {
		t.Errorf("a work with no usable digest was refused as %q", errs.CodeOf(err))
	}
}

func TestEditStampsTheDeviceThatMadeIt(t *testing.T) {
	t.Parallel()

	reader, phone, tablet := uuid.New(), uuid.New(), uuid.New()

	work, err := ebook.New(reader, details(), file(), phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := work.Revision

	corrected := details()
	corrected.Author = "Euclides Rodrigues Pimenta da Cunha"

	if err := work.Edit(corrected, tablet, imported.Add(time.Hour)); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	switch {
	case work.Author != "Euclides Rodrigues Pimenta da Cunha":
		t.Error("the correction was not applied")
	case work.Revision.DeviceID != tablet:
		t.Error("the revision names the device that imported the work rather than the one that edited it")
	case !before.VectorClock.HappensBefore(work.Revision.VectorClock):
		t.Error("the edit does not causally dominate the version it derives from")
	}
}

// The file is fixed at import: an edit that could change the digest would make
// the row describe a file it is not.
func TestEditLeavesTheFileAlone(t *testing.T) {
	t.Parallel()

	work, err := ebook.New(uuid.New(), details(), file(), uuid.New(), imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := work.Edit(details(), uuid.New(), imported.Add(time.Hour)); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if work.Hash != ebook.ContentHash(longHex('a')) || work.Format != ebook.FormatEPUB || work.Size != 1024 {
		t.Error("editing the description changed what the bytes are")
	}
}

func TestDeleteTombstonesRatherThanRemoving(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	work, err := ebook.New(uuid.New(), details(), file(), phone, imported)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := work.Revision
	work.Delete(phone, imported.Add(time.Hour))

	switch {
	case !work.IsDeleted():
		t.Error("the work was not marked removed")
	case !before.VectorClock.HappensBefore(work.Revision.VectorClock):
		t.Error("the deletion does not causally dominate the version it removed, so a node that " +
			"had not heard about it would resurrect the work")
	}
}
