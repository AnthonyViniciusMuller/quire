// Package listdevices serves AuthService.ListDevices (RF11).
package listdevices

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/listdevices"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// ListDevices serves the call.
type ListDevices struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListDevices {
	return &ListDevices{usecase: list}
}

// Handle answers with the caller's devices.
func (c *ListDevices) Handle(
	ctx context.Context,
	request *quirev1.ListDevicesRequest,
) (*quirev1.ListDevicesResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:          identity.UserID,
		IncludeInactive: request.GetIncludeInactive(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ListDevicesResponse{Devices: convert.Devices(output.Devices)}, nil
}
