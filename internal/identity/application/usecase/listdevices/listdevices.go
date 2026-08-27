// Package listdevices answers which appliances write on a reader's behalf.
//
// It is what makes RF11 auditable. A vector clock entry is keyed by a device
// identifier, and this is where those identifiers get a name — which is also why
// the unbound ones are readable on request rather than gone.
package listdevices

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
)

// ListDevices reads devices.
type ListDevices struct {
	devices device.Repository
}

// ListDevices satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListDevices)(nil)

// New returns the use case over its dependencies.
func New(devices device.Repository) *ListDevices {
	return &ListDevices{devices: devices}
}

// Execute reads the reader's devices.
func (l *ListDevices) Execute(ctx context.Context, input Input) (Output, error) {
	devices, err := l.devices.ListByUser(ctx, input.UserID, input.IncludeInactive)
	if err != nil {
		return Output{}, err
	}

	return Output{Devices: devices}, nil
}
