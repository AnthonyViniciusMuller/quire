// Package registerdevice binds an appliance without logging in with it, so that
// an already authenticated application can pair a second one (RF11, UC10).
//
// It issues no session. The appliance that was just bound gets one by logging
// in with the identifier this call returns, which is what keeps a session tied
// to a password the appliance's own holder typed.
package registerdevice

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
)

// RegisterDevice binds devices.
type RegisterDevice struct {
	devices device.Repository
}

// RegisterDevice satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RegisterDevice)(nil)

// New returns the use case over its dependencies.
func New(devices device.Repository) *RegisterDevice {
	return &RegisterDevice{devices: devices}
}

// Execute binds the appliance to the reader the call is made on behalf of.
func (r *RegisterDevice) Execute(ctx context.Context, input Input) (Output, error) {
	name, err := device.ParseName(input.Name)
	if err != nil {
		return Output{}, err
	}

	platform, err := device.ParsePlatform(input.Platform)
	if err != nil {
		return Output{}, err
	}

	appliance, err := device.New(input.UserID, name, platform)
	if err != nil {
		return Output{}, err
	}

	err = r.devices.Create(ctx, appliance)
	if err != nil {
		return Output{}, err
	}

	return Output{Device: appliance}, nil
}
