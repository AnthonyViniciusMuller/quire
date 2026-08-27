package revokedevice_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/revokedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is one reader with two appliances, each holding a session, so that a
// test can tell what a revocation reached from what it left alone (RN07).
type fixture struct {
	usecase     *revokedevice.RevokeDevice
	devices     *apptest.DeviceRepository
	credentials *apptest.CredentialRepository
	reader      uuid.UUID
	phone       *device.Device
	tablet      *device.Device
	stranger    *device.Device
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	devices := apptest.NewDeviceRepository()
	credentials := apptest.NewCredentialRepository()
	reader := uuid.New()

	bind := func(owner uuid.UUID, name device.Name) *device.Device {
		t.Helper()

		appliance, err := device.New(owner, name, "android")
		if err != nil {
			t.Fatalf("device.New: %v", err)
		}

		err = devices.Create(t.Context(), appliance)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		session, err := credential.NewSession(owner, appliance.ID, "digest:"+appliance.ID.String(), now().Add(time.Hour))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		err = credentials.Create(t.Context(), session)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		return appliance
	}

	return fixture{
		usecase:     revokedevice.New(devices, credentials, apptest.NewTransaction()),
		devices:     devices,
		credentials: credentials,
		reader:      reader,
		phone:       bind(reader, "Pixel 9"),
		tablet:      bind(reader, "Tablet"),
		stranger:    bind(uuid.New(), "Somebody else's laptop"),
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 3 {
		t.Fatalf("%d sessions are live before the revocation, want 3", live)
	}

	_, err := f.usecase.Execute(t.Context(), revokedevice.Input{UserID: f.reader, DeviceID: f.phone.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := f.devices.GetByID(t.Context(), f.phone.ID)
	if err != nil {
		t.Fatalf("the device: %v", err)
	}

	// The row survives: every operation the device authored is still keyed by
	// its identifier, and a clock naming a device nobody can resolve cannot be
	// explained to the reader.
	switch {
	case stored.Active:
		t.Error("the device is still bound")
	case stored.ID != f.phone.ID:
		t.Error("revoking the device changed its identifier")
	case stored.Name != "Pixel 9":
		t.Error("revoking the device lost its name, which is what explains a clock entry")
	}

	// Its sessions end, and only its own (RN07).
	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Errorf("%d sessions are live after the revocation, want the other two", live)
	}

	revoked, err := f.credentials.GetByTokenHash(t.Context(), "digest:"+f.phone.ID.String())
	if err != nil {
		t.Fatalf("the revoked device's credential: %v", err)
	}

	if revoked.Usable(now()) {
		t.Error("the unbound device can still refresh, which is what Quadro 17 forbids")
	}

	survivor, err := f.credentials.GetByTokenHash(t.Context(), "digest:"+f.tablet.ID.String())
	if err != nil {
		t.Fatalf("the other device's credential: %v", err)
	}

	if !survivor.Usable(now()) {
		t.Error("unbinding one appliance ended another's session")
	}
}

// TestExecuteIsIdempotent covers the device that is already unbound: what the
// caller asked for holds, so there is nothing to do and nothing to report.
func TestExecuteIsIdempotent(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	input := revokedevice.Input{UserID: f.reader, DeviceID: f.phone.ID}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("the first revocation: %v", err)
	}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Errorf("the second revocation: %v, want it to succeed", err)
	}
}

// TestExecuteRefusesADeviceThatIsNotTheirs is what stops a reader from unbinding
// somebody else's appliance, and it is answered as an identifier nobody has.
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

			_, err := f.usecase.Execute(t.Context(), revokedevice.Input{
				UserID:   f.reader,
				DeviceID: test.deviceID,
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

	stored, err := f.devices.GetByID(t.Context(), f.stranger.ID)
	if err != nil {
		t.Fatalf("the stranger's device: %v", err)
	}

	if !stored.Active {
		t.Error("a stranger's appliance was unbound")
	}
}
