package device

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, owner := uuid.New(), uuid.New()
	synced := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	appliance := toDomain(&identitydb.IdentityDevice{
		ID:           id,
		UserID:       owner,
		Name:         "Pixel 9",
		Platform:     "android",
		LastSyncedAt: &synced,
		Active:       true,
	})

	switch {
	case appliance.ID != id:
		t.Error("the row was rebuilt under a new identifier, so its clock entries would be unattributable")
	case appliance.UserID != owner:
		t.Error("the owner was lost")
	case appliance.Name != "Pixel 9" || appliance.Platform != "android":
		t.Errorf("the description was not carried across: %+v", appliance.Props)
	case !appliance.HasSynced() || !appliance.LastSyncedAt.Equal(synced):
		t.Error("the last synchronization was not carried across")
	case !appliance.Active:
		t.Error("a bound device came back unbound")
	}
}

// TestToDomainOfARevokedDevice covers the row that survives revocation, which
// is what explains a clock entry the reader no longer recognizes.
func TestToDomainOfARevokedDevice(t *testing.T) {
	t.Parallel()

	appliance := toDomain(&identitydb.IdentityDevice{
		ID:       uuid.New(),
		UserID:   uuid.New(),
		Name:     "Old tablet",
		Platform: "android",
		Active:   false,
	})

	if appliance.Active {
		t.Error("a revoked device came back bound")
	}

	if appliance.HasSynced() {
		t.Error("a null last_synced_at came back as a synchronization that happened")
	}
}

func TestOptionalTime(t *testing.T) {
	t.Parallel()

	if got := optionalTime(time.Time{}); got != nil {
		t.Errorf("optionalTime of the zero instant = %s, want NULL", got)
	}

	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	if got := optionalTime(at); got == nil || !got.Equal(at) {
		t.Error("optionalTime dropped an instant that is present")
	}
}
