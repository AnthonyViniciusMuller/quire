package updatedevice

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
)

// Input is a reader renaming one of their appliances.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// DeviceID is the appliance being renamed.
	DeviceID uuid.UUID
	// Name is the new name. It is the only thing about a bound device that is
	// editable: its platform is what it is, and its identifier is referenced by
	// every clock it appears in.
	Name string
}

// Output is the device as it now reads.
type Output struct {
	// Device is the record that was written.
	Device *device.Device
}
