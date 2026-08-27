package membership

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, work, grouping, phone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	filing := toDomain(&librarydb.LibraryEbookCollection{
		ID:           id,
		EbookID:      work,
		CollectionID: grouping,
		VectorClock:  crdt.VectorClock{crdt.Author(phone): 1},
		UpdatedAt:    at,
		DeviceID:     &phone,
	})

	switch {
	case filing.ID != id:
		t.Error("the row was rebuilt under a new identifier, so the pair would have two histories")
	case filing.EbookID != work || filing.CollectionID != grouping:
		t.Error("the filing no longer names the pair it is about")
	case !filing.IsFiled():
		t.Error("a set register came back cleared")
	case filing.Revision.DeviceID != phone:
		t.Error("the revision lost the device whose write the row reflects")
	}
}

// The tombstone is what "not filed" means here, so a cleared register has to
// come back as one rather than as a missing row.
func TestToDomainOfAClearedRegister(t *testing.T) {
	t.Parallel()

	filing := toDomain(&librarydb.LibraryEbookCollection{
		ID:           uuid.New(),
		EbookID:      uuid.New(),
		CollectionID: uuid.New(),
		Deleted:      true,
	})

	if filing.IsFiled() {
		t.Error("a work taken off a shelf came back as still on it")
	}
}
