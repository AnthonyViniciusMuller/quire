package credential

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, owner, appliance := uuid.New(), uuid.New(), uuid.New()
	expires := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)

	issued := toDomain(&identitydb.IdentityCredential{
		ID:        id,
		UserID:    owner,
		DeviceID:  &appliance,
		Kind:      string(credential.KindSessionRefresh),
		TokenHash: "digest",
		ExpiresAt: expires,
	})

	switch {
	case issued.ID != id:
		t.Error("the row was rebuilt under a new identifier")
	case issued.UserID != owner:
		t.Error("the reader was lost")
	case !issued.BelongsToDevice(appliance):
		t.Error("the device was lost, so revoking that device would miss this credential")
	case issued.Kind != credential.KindSessionRefresh:
		t.Errorf("Kind = %q, want %q", issued.Kind, credential.KindSessionRefresh)
	case !issued.Usable(expires.Add(-time.Hour)):
		t.Error("an unconsumed credential inside its validity came back unusable")
	}
}

// TestToDomainOfARecoveryCredential covers the null device: a reader recovering
// a password may be doing it from an appliance that is not bound at all.
func TestToDomainOfARecoveryCredential(t *testing.T) {
	t.Parallel()

	issued := toDomain(&identitydb.IdentityCredential{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		Kind:      string(credential.KindPasswordRecovery),
		TokenHash: "digest",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if issued.BelongsToDevice(uuid.UUID{}) {
		t.Error("a null device_id came back as the zero device rather than as no device")
	}
}

// TestToDomainOfASpentCredential is what stops a consumed credential from being
// honoured a second time after a read.
func TestToDomainOfASpentCredential(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	issued := toDomain(&identitydb.IdentityCredential{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		Kind:      string(credential.KindPasswordRecovery),
		TokenHash: "digest",
		ExpiresAt: now.Add(time.Hour),
		Consumed:  true,
	})

	if issued.Usable(now) {
		t.Error("a credential read back as consumed came back usable")
	}
}

func TestOptionalID(t *testing.T) {
	t.Parallel()

	if got := optionalID(uuid.UUID{}); got != nil {
		t.Errorf("optionalID of the zero identifier = %s, want NULL", got)
	}

	id := uuid.New()
	if got := optionalID(id); got == nil || *got != id {
		t.Error("optionalID dropped an identifier that is present")
	}
}
