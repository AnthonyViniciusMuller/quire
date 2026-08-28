package delivery

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/sync/infra/persist/syncdb"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, change, peer := uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	reason := "no route to host"

	owed := toDomain(&syncdb.SyncDelivery{
		ID:            id,
		OperationID:   change,
		ServerID:      peer,
		Attempts:      3,
		LastAttemptAt: &at,
		LastError:     &reason,
	})

	switch {
	case owed.ID != id || owed.OperationID != change || owed.ServerID != peer:
		t.Errorf("the row was rebuilt naming a different pair: %+v", owed.Props)
	case !owed.IsPending():
		t.Error("a row still owed came back confirmed")
	case owed.Attempts != 3:
		t.Errorf("the row came back having been tried %d times", owed.Attempts)
	case owed.LastError != reason:
		t.Errorf("the last failure came back as %q", owed.LastError)
	}
}

// A row that has never been tried and one whose last try succeeded both carry
// no failure, and the column holds both as NULL.
func TestToDomainOfARowWithNoFailureToReport(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

	owed := toDomain(&syncdb.SyncDelivery{
		ID:          uuid.New(),
		OperationID: uuid.New(),
		ServerID:    uuid.New(),
		AppliedAt:   &at,
		Attempts:    1,
	})

	if owed.IsPending() {
		t.Error("a confirmed row came back still owed")
	}

	if owed.LastError != "" {
		t.Errorf("a row with no failure came back reporting %q", owed.LastError)
	}
}
