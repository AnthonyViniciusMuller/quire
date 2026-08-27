package device_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestNew(t *testing.T) {
	t.Parallel()

	owner := uuid.New()

	appliance, err := device.New(owner, "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	switch {
	case appliance.ID == (uuid.UUID{}):
		t.Error("the device was given no identifier, and a vector clock entry is keyed by it")
	case appliance.UserID != owner:
		t.Error("the device was not bound to the reader that asked")
	case !appliance.Active:
		t.Error("a device is bound the moment it is created")
	case appliance.HasSynced():
		t.Error("a device that has never synchronized reported that it had")
	}
}

// TestNewIdentifiersAreDistinct is what makes a vector clock resolvable: two
// devices of the same reader must never share an entry.
func TestNewIdentifiersAreDistinct(t *testing.T) {
	t.Parallel()

	owner := uuid.New()

	first, err := device.New(owner, "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	second, err := device.New(owner, "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	if first.ID == second.ID {
		t.Error("two devices of the same reader share an identifier, so their clocks would merge into one entry")
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		owner    uuid.UUID
		device   device.Name
		platform device.Platform
		code     string
		field    string
	}{
		{
			name: "no owner", owner: uuid.UUID{}, device: "Pixel 9", platform: "android",
			code: device.CodeInvalidDevice, field: "user_id",
		},
		{
			name: "a blank name", owner: uuid.New(), device: "", platform: "android",
			code: device.CodeInvalidName, field: "name",
		},
		{
			name: "a name over the column", owner: uuid.New(),
			device: device.Name(strings.Repeat("a", 121)), platform: "android",
			code: device.CodeInvalidName, field: "name",
		},
		{
			name: "a blank platform", owner: uuid.New(), device: "Pixel 9", platform: "",
			code: device.CodeInvalidPlatform, field: "platform",
		},
		{
			name: "a platform over the column", owner: uuid.New(), device: "Pixel 9",
			platform: device.Platform(strings.Repeat("a", 41)),
			code:     device.CodeInvalidPlatform, field: "platform",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := device.New(test.owner, test.device, test.platform)
			if err == nil {
				t.Fatalf("device.New = %+v, want an error", got)
			}

			assertInvalidArgument(t, err, test.code, test.field)
		})
	}
}

func TestParseTrimsTheSurroundingSpace(t *testing.T) {
	t.Parallel()

	name, err := device.ParseName("  Pixel 9 ")
	if err != nil {
		t.Fatalf("ParseName: %v", err)
	}

	if name != "Pixel 9" {
		t.Errorf("ParseName = %q, want it trimmed", name)
	}

	platform, err := device.ParsePlatform(" android ")
	if err != nil {
		t.Fatalf("ParsePlatform: %v", err)
	}

	if platform != "android" {
		t.Errorf("ParsePlatform = %q, want it trimmed", platform)
	}

	if _, err := device.ParseName("   "); err == nil {
		t.Error("ParseName of only space = nil, want an error")
	}
}

// TestRevokeKeepsTheDevice covers why unbinding clears a flag: every operation
// the device authored still names its id, and a clock entry pointing at a
// device nobody can resolve cannot be explained to the reader.
func TestRevokeKeepsTheDevice(t *testing.T) {
	t.Parallel()

	appliance, err := device.New(uuid.New(), "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	id := appliance.ID
	appliance.Revoke()

	if appliance.Active {
		t.Error("the device is still bound after being revoked")
	}

	if appliance.ID != id {
		t.Error("revoking the device changed its identifier")
	}
}

// TestRestoreKeepsTheIdentifier is what a repository needs: the id read back is
// the one every clock entry of this appliance is keyed by.
func TestRestoreKeepsTheIdentifier(t *testing.T) {
	t.Parallel()

	id := uuid.New()

	restored := device.Restore(id, &device.Props{
		UserID:   uuid.New(),
		Name:     "Pixel 9",
		Platform: "android",
		Active:   false,
	})

	if restored.ID != id {
		t.Error("Restore minted a new identifier over the one that was read")
	}

	if restored.Active {
		t.Error("Restore rebound a device that had been revoked")
	}
}

func TestRenameAndSync(t *testing.T) {
	t.Parallel()

	appliance, err := device.New(uuid.New(), "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	if err := appliance.Rename("Tablet"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if appliance.Name != "Tablet" {
		t.Errorf("Name = %q, want %q", appliance.Name, "Tablet")
	}

	if err := appliance.Rename(""); err == nil {
		t.Error("Rename to a blank name = nil, want an error")
	}

	if appliance.Name != "Tablet" {
		t.Errorf("Name = %q after a rejected rename, want it unchanged", appliance.Name)
	}

	synced := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	appliance.MarkSynced(synced)

	if !appliance.HasSynced() || !appliance.LastSyncedAt.Equal(synced) {
		t.Errorf("LastSyncedAt = %s, want %s", appliance.LastSyncedAt, synced)
	}
}

// assertInvalidArgument checks that err is the kind, code and named field a
// client is expected to be able to act on.
func assertInvalidArgument(t *testing.T, err error, code, field string) {
	t.Helper()

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if got := errs.CodeOf(err); got != code {
		t.Errorf("code = %q, want %q", got, code)
	}

	fields := errs.FieldsOf(err)
	if len(fields) == 0 {
		t.Fatalf("error %v names no field, so a client cannot point at the input", err)
	}

	if fields[0].Name != field {
		t.Errorf("field = %q, want %q", fields[0].Name, field)
	}

	if fields[0].Reason == "" {
		t.Error("the named field carries no reason")
	}
}
