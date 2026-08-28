package delivery_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/delivery"
)

// tried is when the attempts below were made.
var tried = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

func TestNew(t *testing.T) {
	t.Parallel()

	change, peer := uuid.New(), uuid.New()

	owed, err := delivery.New(change, peer)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case owed.ID == (uuid.UUID{}):
		t.Error("the delivery was recorded without an identifier, so the row could not be named")
	case owed.OperationID != change || owed.ServerID != peer:
		t.Errorf("the delivery does not name the pair it is for: %+v", owed.Props)
	case !owed.IsPending():
		t.Error("a delivery was created already confirmed, which is not a state it can be created in")
	case owed.Attempts != 0 || owed.LastAttemptAt != nil:
		t.Error("a delivery was created having already been tried")
	}
}

func TestNewRefusesAPairThatNamesNothing(t *testing.T) {
	t.Parallel()

	for name, pair := range map[string][2]uuid.UUID{
		"no change": {{}, uuid.New()},
		"no node":   {uuid.New(), {}},
	} {
		if _, err := delivery.New(pair[0], pair[1]); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("%s: New = %v, want an invalid argument", name, err)
		}
	}
}

// A failure has to be counted and not merely logged: the count is what the
// backoff is computed from, and a failure nobody counted is a peer retried at
// full rate for ever.
func TestRecordCountsAFailureAndKeepsTheRowInTheQueue(t *testing.T) {
	t.Parallel()

	owed, err := delivery.New(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	owed.Record(&delivery.Attempt{At: tried, Err: errors.New("no route to host")})

	switch {
	case !owed.IsPending():
		t.Error("a failed try closed the delivery")
	case owed.Attempts != 1:
		t.Errorf("the delivery has been tried %d times, want 1", owed.Attempts)
	case owed.LastAttemptAt == nil || !owed.LastAttemptAt.Equal(tried):
		t.Errorf("the try was not recorded at %s", tried)
	case owed.LastError != "no route to host":
		t.Errorf("the failure came back as %q", owed.LastError)
	}
}

// A confirmation is final, and it clears what the last failure said: a
// delivery that succeeded is not a delivery that failed a while ago.
func TestRecordClosesTheRowOnAConfirmation(t *testing.T) {
	t.Parallel()

	owed, err := delivery.New(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	owed.Record(&delivery.Attempt{At: tried, Err: errors.New("connection refused")})
	owed.Record(&delivery.Attempt{At: tried.Add(time.Minute)})

	switch {
	case owed.IsPending():
		t.Error("a confirmed delivery is still owed")
	case owed.Attempts != 2:
		t.Errorf("the delivery has been tried %d times, want 2", owed.Attempts)
	case owed.LastError != "":
		t.Errorf("the confirmed delivery still reports %q", owed.LastError)
	case owed.AppliedAt == nil || !owed.AppliedAt.Equal(tried.Add(time.Minute)):
		t.Error("the delivery was not confirmed at the instant the try was made")
	}
}
