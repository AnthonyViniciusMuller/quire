package listdevices_test

import (
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/listdevices"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
)

// fixture is one reader with three appliances — two bound and one unbound — and
// a second reader with one, so that a test can tell whose devices come back.
type fixture struct {
	usecase   *listdevices.ListDevices
	reader    uuid.UUID
	stranger  uuid.UUID
	revokedID uuid.UUID
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	devices := apptest.NewDeviceRepository()
	reader, stranger := uuid.New(), uuid.New()

	bind := func(owner uuid.UUID, name device.Name, active bool) uuid.UUID {
		t.Helper()

		appliance, err := device.New(owner, name, "android")
		if err != nil {
			t.Fatalf("device.New: %v", err)
		}

		if !active {
			appliance.Revoke()
		}

		if err := devices.Create(t.Context(), appliance); err != nil {
			t.Fatalf("Create: %v", err)
		}

		return appliance.ID
	}

	bind(reader, "Tablet", true)
	bind(reader, "Pixel 9", true)
	revoked := bind(reader, "An old phone", false)
	bind(stranger, "Somebody else's laptop", true)

	return fixture{
		usecase:   listdevices.New(devices),
		reader:    reader,
		stranger:  stranger,
		revokedID: revoked,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), listdevices.Input{UserID: f.reader})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Devices) != 2 {
		t.Fatalf("%d devices came back, want the reader's two bound ones", len(output.Devices))
	}

	// Ordered by name, so that the list does not reshuffle between two calls.
	if output.Devices[0].Name != "Pixel 9" || output.Devices[1].Name != "Tablet" {
		t.Errorf("the devices came back as %q then %q, want them ordered by name",
			output.Devices[0].Name, output.Devices[1].Name)
	}

	for _, appliance := range output.Devices {
		if !appliance.BelongsTo(f.reader) {
			t.Error("a device of another reader came back")
		}
	}
}

// TestExecuteIncludesTheUnboundOnRequest is why revocation keeps the row: it is
// what explains a clock entry the reader no longer recognizes.
func TestExecuteIncludesTheUnboundOnRequest(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), listdevices.Input{UserID: f.reader, IncludeInactive: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Devices) != 3 {
		t.Fatalf("%d devices came back, want all three", len(output.Devices))
	}

	var found bool

	for _, appliance := range output.Devices {
		if appliance.ID == f.revokedID {
			found = true

			if appliance.Active {
				t.Error("the unbound device came back bound")
			}
		}
	}

	if !found {
		t.Error("the unbound device was not listed even though it was asked for")
	}
}

// TestExecuteForAReaderWithNoDevices is what the empty answer has to be: an
// empty list, never nil, so a client that ranges over it behaves the same.
func TestExecuteForAReaderWithNoDevices(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), listdevices.Input{UserID: uuid.New()})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Devices == nil {
		t.Error("the list is nil rather than empty")
	}

	if len(output.Devices) != 0 {
		t.Errorf("%d devices came back for a reader with none", len(output.Devices))
	}
}
