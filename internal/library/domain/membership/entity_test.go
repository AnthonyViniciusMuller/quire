package membership_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// filed is the instant the filings below were made at.
var filed = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func TestNew(t *testing.T) {
	t.Parallel()

	work, grouping, phone := uuid.New(), uuid.New(), uuid.New()

	filing, err := membership.New(work, grouping, phone, filed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case filing.ID == (uuid.UUID{}):
		t.Error("the filing was recorded without an identifier")
	case filing.EbookID != work || filing.CollectionID != grouping:
		t.Error("the filing does not name the pair it is about")
	case !filing.IsFiled():
		t.Error("a work that was just filed is not on the shelf")
	}
}

func TestNewRefusesAFilingThatNamesNothing(t *testing.T) {
	t.Parallel()

	work, grouping, phone := uuid.New(), uuid.New(), uuid.New()

	cases := map[string]struct {
		ebookID, collectionID, device uuid.UUID
		at                            time.Time
		field                         string
	}{
		"no work":     {uuid.UUID{}, grouping, phone, filed, "ebook_id"},
		"no grouping": {work, uuid.UUID{}, phone, filed, "collection_id"},
		"no device":   {work, grouping, uuid.UUID{}, filed, "device_id"},
		"no time":     {work, grouping, phone, time.Time{}, "updated_at"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := membership.New(testCase.ebookID, testCase.collectionID, testCase.device, testCase.at)
			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Fatalf("New = %v, want an invalid argument", err)
			}

			if fields := errs.FieldsOf(err); len(fields) != 1 || fields[0].Name != testCase.field {
				t.Errorf("the refusal points at %v, want %s", fields, testCase.field)
			}
		})
	}
}

func TestSetAndClearAreARegister(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	filing, err := membership.New(uuid.New(), uuid.New(), phone, filed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if changed := filing.Clear(phone, filed.Add(time.Hour)); !changed {
		t.Error("unfiling a work that was on the shelf reported no change")
	}

	if filing.IsFiled() {
		t.Error("the work is still on the shelf after being taken off it")
	}

	if changed := filing.Set(phone, filed.Add(2*time.Hour)); !changed {
		t.Error("filing a work that was off the shelf reported no change")
	}

	if !filing.IsFiled() {
		t.Error("the work is not on the shelf after being put back")
	}
}

// The call is idempotent to the reader and is not a no-op to replication: a
// write that left the revision alone would let an older removal from another
// device win a tie-break it should have lost.
func TestSettingAnAlreadySetRegisterStillStampsAWrite(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	filing, err := membership.New(uuid.New(), uuid.New(), phone, filed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := filing.Revision

	if changed := filing.Set(phone, filed.Add(time.Hour)); changed {
		t.Error("filing a work that was already filed reported a change to the reader")
	}

	if !before.VectorClock.HappensBefore(filing.Revision.VectorClock) {
		t.Error("the write was not recorded in the causal history, so replication would not carry it")
	}
}
