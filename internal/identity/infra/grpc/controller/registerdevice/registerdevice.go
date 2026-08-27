// Package registerdevice serves AuthService.RegisterDevice (RF11, UC10).
package registerdevice

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/registerdevice"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// RegisterDevice serves the call.
type RegisterDevice struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(register command.Usecase[usecase.Input, usecase.Output]) *RegisterDevice {
	return &RegisterDevice{usecase: register}
}

// Handle binds the appliance to the caller's account.
func (c *RegisterDevice) Handle(
	ctx context.Context,
	request *quirev1.RegisterDeviceRequest,
) (*quirev1.RegisterDeviceResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		Name:     request.GetName(),
		Platform: request.GetPlatform(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.RegisterDeviceResponse{Device: convert.Device(output.Device)}, nil
}
