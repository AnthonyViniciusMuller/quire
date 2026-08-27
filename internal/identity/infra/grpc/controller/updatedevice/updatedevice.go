// Package updatedevice serves AuthService.UpdateDevice.
package updatedevice

import (
	"context"
	"uuid"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updatedevice"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opHandle is the operation reported by this file, in the form the errs package
// expects.
const opHandle = "identity/updatedevice: handle"

// CodeUnwritableField is a mask naming a field this call does not write.
const CodeUnwritableField = "unwritable_field"

// pathName is the only path the mask may carry: the platform is what it is, and
// the identifier is referenced by every clock the device appears in.
const pathName = "name"

// UpdateDevice serves the call.
type UpdateDevice struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateDevice {
	return &UpdateDevice{usecase: update}
}

// Handle renames the appliance.
func (c *UpdateDevice) Handle(
	ctx context.Context,
	request *quirev1.UpdateDeviceRequest,
) (*quirev1.UpdateDeviceResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	// A malformed identifier is answered as one nobody has, for the reason the
	// use case answers a stranger's device that way: the reply must not be an
	// oracle for which identifiers exist.
	deviceID, err := uuid.Parse(request.GetDeviceId())
	if err != nil {
		return nil, errs.Wrap(err, errs.KindNotFound, "that device is not bound to this account").
			WithOp(opHandle).
			WithCode(device.CodeNotFound)
	}

	input := usecase.Input{UserID: identity.UserID, DeviceID: deviceID}

	for _, path := range request.GetUpdateMask().GetPaths() {
		if path != pathName {
			return nil, errs.Newf(errs.KindInvalidArgument, "%q is not a field this call writes", path).
				WithOp(opHandle).
				WithCode(CodeUnwritableField).
				WithField("update_mask", "only name may be changed")
		}

		input.Name = request.GetDevice().GetName()
	}

	output, err := c.usecase.Execute(ctx, input)
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateDeviceResponse{Device: convert.Device(output.Device)}, nil
}
