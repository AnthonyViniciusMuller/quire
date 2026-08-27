package annotation_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// written is when the marks below were made.
var written = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// note is a well-formed mark.
func note() *annotation.Mark {
	return &annotation.Mark{
		Kind:    annotation.KindNote,
		Text:    "a sertão é uma sociedade",
		Locator: locator.Locator("epubcfi(/6/14!/4/10/3:10)"),
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	work, phone := uuid.New(), uuid.New()

	mark, err := annotation.New(work, note(), phone, written)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case mark.ID == (uuid.UUID{}):
		t.Error("the mark was recorded without an identifier the reply could carry")
	case !mark.IsIn(work):
		t.Error("the mark does not name the work it was made in")
	case mark.Revision.DeviceID != phone:
		t.Error("the revision does not name the device that made the mark")
	case mark.Revision.VectorClock.Get(crdt.Author(phone)) != 1:
		t.Error("the causal history does not count the mark as an event of the writing device")
	case mark.IsDeleted():
		t.Error("a mark that has just been made is tombstoned")
	}
}

// A note is the text: reading.annotations_note_has_text refuses one with
// nothing in it, and the entity has to refuse it first or the row is rejected
// after the constructor accepted it.
func TestNewRefusesWhatCannotBeAMark(t *testing.T) {
	t.Parallel()

	work, phone := uuid.New(), uuid.New()

	tests := map[string]func(*annotation.Mark){
		"an unknown kind":       func(m *annotation.Mark) { m.Kind = "underline" },
		"no kind":               func(m *annotation.Mark) { m.Kind = "" },
		"no passage":            func(m *annotation.Mark) { m.Locator = "" },
		"a note saying nothing": func(m *annotation.Mark) { m.Text = "" },
		"a note of only space":  func(m *annotation.Mark) { m.Text = "   " },
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mark := note()
			breaks(mark)

			if _, err := annotation.New(work, mark, phone, written); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument", err)
			}
		})
	}
}

// A mark made by an appliance that was never bound would introduce a causal
// history no node could attribute to anybody (RN10).
func TestNewRefusesAMarkNobodyCanBeAttributedWith(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		work, device uuid.UUID
		at           time.Time
	}{
		"no work":   {uuid.UUID{}, uuid.New(), written},
		"no device": {uuid.New(), uuid.UUID{}, written},
		"no instant": {
			uuid.New(), uuid.New(), time.Time{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := annotation.New(test.work, note(), test.device, test.at)
			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument", err)
			}
		})
	}
}

// A highlight and a bookmark are about the passage, and carry text only if the
// reader gave them one.
func TestNewAcceptsAMarkWithNothingWrittenOnIt(t *testing.T) {
	t.Parallel()

	for _, kind := range []annotation.Kind{annotation.KindHighlight, annotation.KindBookmark} {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()

			mark := note()
			mark.Kind = kind
			mark.Text = ""

			if _, err := annotation.New(uuid.New(), mark, uuid.New(), written); err != nil {
				t.Errorf("New: %v", err)
			}
		})
	}
}

// C10: after an edit from a second device, the device the row names and the
// device that created the mark are different, and the tie-break needs the
// first.
func TestEditNamesTheDeviceWhoseWriteTheRowReflects(t *testing.T) {
	t.Parallel()

	phone, tablet := uuid.New(), uuid.New()

	mark, err := annotation.New(uuid.New(), note(), phone, written)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	edited := note()
	edited.Text = "o sertanejo é, antes de tudo, um forte"

	if err = mark.Edit(edited, tablet, written.Add(time.Minute)); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	switch {
	case mark.Revision.DeviceID != tablet:
		t.Error("the revision still names the device that created the mark")
	case mark.Text != edited.Text:
		t.Errorf("the mark reads %q", mark.Text)
	case mark.Revision.VectorClock.Get(crdt.Author(phone)) != 1:
		t.Error("the edit dropped the creating device from the causal history")
	case mark.Revision.VectorClock.Get(crdt.Author(tablet)) != 1:
		t.Error("the causal history does not count the edit as an event of the editing device")
	}
}

func TestEditRefusesWhatCannotBeAMark(t *testing.T) {
	t.Parallel()

	mark, err := annotation.New(uuid.New(), note(), uuid.New(), written)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	broken := note()
	broken.Text = ""

	if err = mark.Edit(broken, uuid.New(), written); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Edit = %v, want an invalid argument", err)
	}

	if err = mark.Edit(note(), uuid.UUID{}, written); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Edit without a device = %v, want an invalid argument", err)
	}

	if mark.Revision.VectorClock.Get(crdt.Author(mark.Revision.DeviceID)) != 1 {
		t.Error("a refused edit stamped a write")
	}
}

// The row stays and the tombstone travels, or the next node that had not heard
// about the deletion resurrects the mark by replying with its own copy.
func TestDeleteTombstonesRatherThanRemoves(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	mark, err := annotation.New(uuid.New(), note(), phone, written)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mark.Delete(phone, written.Add(time.Minute))

	switch {
	case !mark.IsDeleted():
		t.Error("the mark was not tombstoned")
	case mark.Revision.VectorClock.Get(crdt.Author(phone)) != 2:
		t.Error("the deletion was not counted as a write")
	case !mark.Revision.UpdatedAt.After(written):
		t.Error("the deletion did not stamp an instant after the write it follows")
	case mark.Text.IsZero():
		t.Error("the tombstone discarded what the mark said, which a peer still has to reconcile")
	}
}

// Restoring must not mint an identifier: the id is the one the client holds
// and the one an operation names.
func TestRestoreKeepsTheIdentifier(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	props := annotation.Props{EbookID: uuid.New(), Mark: *note()}

	if got := annotation.Restore(id, &props); got.ID != id {
		t.Errorf("Restore minted %s in place of %s", got.ID, id)
	}
}
