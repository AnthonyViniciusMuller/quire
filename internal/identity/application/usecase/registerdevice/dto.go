package registerdevice

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
)

// Input is an appliance asking to be bound without logging in.
type Input struct {
	// UserID is the reader the call is made on behalf of. It comes from the
	// authenticated session and never from the request body, which is what
	// stops a reader from binding an appliance to somebody else's account.
	UserID uuid.UUID
	// Name is what the reader calls this appliance.
	Name string
	// Platform is the operating system it runs.
	Platform string
}

// Output is the device that was bound.
type Output struct {
	// Device carries the identifier the appliance must use in its vector
	// clocks, which is the whole reason the call exists.
	Device *device.Device
}
