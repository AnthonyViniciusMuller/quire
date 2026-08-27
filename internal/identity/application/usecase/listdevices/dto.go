package listdevices

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
)

// Input is a reader asking which appliances write on their behalf.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// IncludeInactive asks for the appliances that have been unbound as well.
	// They are hidden by default and readable on request: their rows survive
	// revocation, and they are what explains a clock entry the reader no longer
	// recognizes.
	IncludeInactive bool
}

// Output is the reader's devices.
//
// It is not paginated, and the contract says why: a reader has as many devices
// as they have hands, and a page token here would be a parameter no client ever
// sets.
type Output struct {
	// Devices are ordered by name, so that the list does not reshuffle between
	// two calls.
	Devices []*device.Device
}
