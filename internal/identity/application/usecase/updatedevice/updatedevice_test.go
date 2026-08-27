package updatedevice_test

import (
	"errors"
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updatedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// fixture is one reader with an appliance, and a stranger with another.
type fixture struct {
	usecase  *updatedevice.UpdateDevice
	devices  *apptest.DeviceRepository
	reader   uuid.UUID
	own      *device.Device
	stranger *device.Device
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	devices := apptest.NewDeviceRepository()
	reader := uuid.New()

	bind := func(owner uuid.UUID, name device.Name) *device.Device {
		t.Helper()

		appliance, err := device.New(owner, name, "android")
		if err != nil {
			t.Fatalf("device.New: %v", err)
		}

		if err := devices.Create(t.Context(), appliance); err != nil {
			t.Fatalf("Create: %v", err)
		}

		return appliance
	}

	return fixture{
		usecase:  updatedevice.New(devices),
		devices:  devices,
		reader:   reader,
		own:      bind(reader, "Pixel 9"),
		stranger: bind(uuid.New(), "Somebody else's laptop"),
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), updatedevice.Input{
		UserID:   f.reader,
		DeviceID: f.own.ID,
		Name:     "  The work phone ",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Device.Name != "The work phone":
		t.Errorf("Name = %q, want it trimmed", output.Device.Name)
	case output.Device.ID != f.own.ID:
		t.Error("renaming the device changed its identifier, which every clock entry is keyed by")
	case output.Device.Platform != f.own.Platform:
		t.Error("the platform changed, and it is not editable")
	}

	stored, err := f.devices.GetByID(t.Context(), f.own.ID)
	if err != nil {
		t.Fatalf("the device: %v", err)
	}

	if stored.Name != "The work phone" {
		t.Errorf("the stored name is %q, want the new one", stored.Name)
	}
}

// TestExecuteRenamesARevokedDevice is what keeping the row is for: being able to
// label it "the tablet I lost" is how a reader explains a clock entry to
// themselves.
func TestExecuteRenamesARevokedDevice(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	f.own.Revoke()

	err := f.devices.Update(t.Context(), f.own)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	output, err := f.usecase.Execute(t.Context(), updatedevice.Input{
		UserID:   f.reader,
		DeviceID: f.own.ID,
		Name:     "The tablet I lost",
	})
	if err != nil {
		t.Fatalf("renaming a revoked device: %v", err)
	}

	if output.Device.Active {
		t.Error("renaming the device rebound it")
	}
}

// TestExecuteRefusesADeviceThatIsNotTheirs answers a stranger's device exactly
// as an identifier nobody has, so that the reply is not an oracle.
func TestExecuteRefusesADeviceThatIsNotTheirs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	tests := []struct {
		name     string
		deviceID uuid.UUID
	}{
		{name: "another reader's device", deviceID: f.stranger.ID},
		{name: "an identifier nobody has", deviceID: uuid.New()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := f.usecase.Execute(t.Context(), updatedevice.Input{
				UserID:   f.reader,
				DeviceID: test.deviceID,
				Name:     "Mine now",
			})
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if !errors.Is(err, errs.KindNotFound) {
				t.Errorf("error = %v, want not found", err)
			}

			if code := errs.CodeOf(err); code != device.CodeNotFound {
				t.Errorf("code = %q, want %q for both", code, device.CodeNotFound)
			}
		})
	}

	// And the stranger's device is untouched.
	stored, err := f.devices.GetByID(t.Context(), f.stranger.ID)
	if err != nil {
		t.Fatalf("the stranger's device: %v", err)
	}

	if stored.Name != "Somebody else's laptop" {
		t.Errorf("the stranger's device is now called %q", stored.Name)
	}
}

func TestExecuteRejectsABlankName(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), updatedevice.Input{
		UserID:   f.reader,
		DeviceID: f.own.ID,
		Name:     "   ",
	})
	if err == nil {
		t.Fatal("renaming to nothing = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	stored, err := f.devices.GetByID(t.Context(), f.own.ID)
	if err != nil {
		t.Fatalf("the device: %v", err)
	}

	if stored.Name != "Pixel 9" {
		t.Errorf("a refused rename wrote %q", stored.Name)
	}
}
