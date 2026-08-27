package crdtpb_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/crdtpb"
)

// at is the instant the records below were stamped at.
var at = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// A device absent from a clock and a device mapped to zero are the same causal
// history, and sending both forms would make them compare unequal on the node
// that received them.
func TestVectorClockDropsTheZeroEntries(t *testing.T) {
	t.Parallel()

	phone, tablet := uuid.New(), uuid.New()

	rendered := crdtpb.VectorClock(crdt.VectorClock{
		crdt.Author(phone):  2,
		crdt.Author(tablet): 0,
	})

	if len(rendered.GetEntries()) != 1 {
		t.Errorf("the clock rendered as %v, want the zero entry dropped", rendered.GetEntries())
	}

	if rendered.GetEntries()[string(crdt.Author(phone))] != 2 {
		t.Error("the entry that counts events was not carried across")
	}
}

// A client that ranges over the entries has to behave the same on a record
// nothing has written, so the message and its map are always there.
func TestVectorClockOfARecordNothingHasWritten(t *testing.T) {
	t.Parallel()

	rendered := crdtpb.VectorClock(nil)

	if rendered == nil || rendered.GetEntries() == nil {
		t.Error("an empty clock rendered as an absent one")
	}
}

// The unit is the microsecond, which is what a timestamptz stores and what the
// value has already been truncated to.
func TestTimestampCarriesTheInstantTheColumnKeeps(t *testing.T) {
	t.Parallel()

	if got := crdtpb.Timestamp(at).GetUnixMicros(); got != at.UnixMicro() {
		t.Errorf("the instant rendered as %d, want %d", got, at.UnixMicro())
	}
}

func TestRevisionCarriesBothHalvesOfTheTieBreak(t *testing.T) {
	t.Parallel()

	phone := uuid.New()

	rendered := crdtpb.Revision(crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 2},
		UpdatedAt:   at,
		DeviceID:    phone,
		Deleted:     true,
	})

	switch {
	case rendered.GetUpdatedAt().GetUnixMicros() != at.UnixMicro():
		t.Error("the tie-break timestamp was not carried across")
	case rendered.GetDeviceId() != phone.String():
		t.Error("the tie-break lost its second half")
	case !rendered.GetDeleted():
		t.Error("the tombstone was not carried across, so a peer would resurrect the record")
	}
}
