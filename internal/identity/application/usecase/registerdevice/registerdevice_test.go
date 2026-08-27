package registerdevice_test

import (
	"errors"
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/registerdevice"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestExecute(t *testing.T) {
	t.Parallel()

	devices := apptest.NewDeviceRepository()
	usecase := registerdevice.New(devices)
	reader := uuid.New()

	output, err := usecase.Execute(t.Context(), registerdevice.Input{
		UserID:   reader,
		Name:     "  Tablet ",
		Platform: " android ",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Device.ID == (uuid.UUID{}):
		t.Fatal("no identifier was minted, and it is what the appliance keys its vector clock by")
	case !output.Device.BelongsTo(reader):
		t.Error("the device was bound to another reader")
	case output.Device.Name != "Tablet" || output.Device.Platform != "android":
		t.Errorf("the device was bound as %+v, want it trimmed", output.Device.Props)
	case !output.Device.Active:
		t.Error("the device was bound inactive")
	}

	stored, err := devices.GetByID(t.Context(), output.Device.ID)
	if err != nil {
		t.Fatalf("the device was not written: %v", err)
	}

	if stored.ID != output.Device.ID {
		t.Error("the device that was written is not the one that was returned")
	}
}

// TestExecuteRejectsAnUnusableDescription covers what a device has to say about
// itself, since a device named nothing cannot be told from another in the list
// RF11 makes auditable.
func TestExecuteRejectsAnUnusableDescription(t *testing.T) {
	t.Parallel()

	devices := apptest.NewDeviceRepository()
	usecase := registerdevice.New(devices)

	tests := []struct {
		name     string
		device   string
		platform string
	}{
		{name: "a blank name", device: "   ", platform: "android"},
		{name: "a blank platform", device: "Tablet", platform: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := usecase.Execute(t.Context(), registerdevice.Input{
				UserID:   uuid.New(),
				Name:     test.device,
				Platform: test.platform,
			})
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("error = %v, want an invalid argument", err)
			}
		})
	}

	if devices.Count() != 0 {
		t.Error("a refused registration wrote a device")
	}
}
