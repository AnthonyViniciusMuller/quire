package revokedevice

import "uuid"

// Input is a reader unbinding one of their appliances.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// DeviceID is the appliance being unbound.
	DeviceID uuid.UUID
}

// Output is empty: what the caller asked for is that the device stop writing,
// and there is nothing to report about it beyond that it did.
type Output struct{}
