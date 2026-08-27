package progress

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/persist/readingdb"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, work, phone := uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	percent := 40.13

	position := toDomain(&readingdb.ReadingProgress{
		ID:          id,
		EbookID:     work,
		DeviceID:    phone,
		Locator:     "page=42",
		Percent:     &percent,
		VectorClock: crdt.VectorClock{crdt.Author(phone): 3},
		UpdatedAt:   at,
	})

	switch {
	case position.ID != id:
		t.Error("the row was rebuilt under a new identifier, which the operation naming it would miss")
	case position.EbookID != work || !position.BelongsTo(phone):
		t.Error("the row lost half of the pair that addresses it")
	case position.Locator.String() != "page=42":
		t.Errorf("the reader came back at %q", position.Locator)
	case !position.Percent.IsKnown() || position.Percent.Float64() != percent:
		t.Errorf("the proportion came back as %+v", position.Percent)
	case position.Version.VectorClock.Get(crdt.Author(phone)) != 3:
		t.Error("the causal history was lost, so replication would not recognize a position it has")
	case !position.Version.UpdatedAt.Equal(at):
		t.Errorf("the position was stamped %s, want %s", position.Version.UpdatedAt, at)
	}
}

// NULL is a client that could not compute a proportion, which is a different
// claim from a reader who has read none of the work — and the column is
// nullable so that the two can be told apart.
func TestToDomainOfAPositionWithNoProportion(t *testing.T) {
	t.Parallel()

	position := toDomain(&readingdb.ReadingProgress{
		ID:       uuid.New(),
		EbookID:  uuid.New(),
		DeviceID: uuid.New(),
		Locator:  "page=1",
	})

	if position.Percent.IsKnown() {
		t.Error("a NULL proportion came back as a claim about how far the reader has read")
	}

	zero := 0.0
	atTheStart := toDomain(&readingdb.ReadingProgress{
		ID:       uuid.New(),
		EbookID:  uuid.New(),
		DeviceID: uuid.New(),
		Locator:  "page=1",
		Percent:  &zero,
	})

	if !atTheStart.Percent.IsKnown() {
		t.Error("a reader at the very start of a work came back as one who said nothing")
	}
}

func TestOptionalPercentRendersAbsenceAsNull(t *testing.T) {
	t.Parallel()

	if optionalPercent(progress.NoPercent()) != nil {
		t.Error("a client that computed no proportion had one stored for it")
	}

	known, err := progress.NewPercent(0)
	if err != nil {
		t.Fatalf("NewPercent: %v", err)
	}

	if value := optionalPercent(known); value == nil || *value != 0 {
		t.Error("a reader at the very start of a work was stored as one who said nothing")
	}
}
