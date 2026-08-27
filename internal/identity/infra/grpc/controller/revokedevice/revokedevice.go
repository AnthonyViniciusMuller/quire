// Package revokedevice serves AuthService.RevokeDevice.
package revokedevice

import (
	"context"
	"uuid"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/revokedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs package
// expects.
const opHandle = "identity/revokedevice: handle"

// RevokeDevice serves the call.
type RevokeDevice struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(revoke command.Usecase[usecase.Input, usecase.Output]) *RevokeDevice {
	return &RevokeDevice{usecase: revoke}
}

// Handle unbinds the appliance and ends its sessions.
func (c *RevokeDevice) Handle(
	ctx context.Context,
	request *quirev1.RevokeDeviceRequest,
) (*quirev1.RevokeDeviceResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	deviceID, err := uuid.Parse(request.GetDeviceId())
	if err != nil {
		return nil, errs.Wrap(err, errs.KindNotFound, "that device is not bound to this account").
			WithOp(opHandle).
			WithCode(device.CodeNotFound)
	}

	_, err = c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, DeviceID: deviceID})
	if err != nil {
		return nil, err
	}

	return &quirev1.RevokeDeviceResponse{}, nil
}
