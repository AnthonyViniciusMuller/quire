// Package updatedevice renames one of a reader's appliances.
//
// The name is the only editable thing about a bound device. Its platform is
// what it is, and its identifier is referenced by every vector clock it appears
// in — so a call that could change either would be a call that rewrites history.
package updatedevice

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/updatedevice: execute"

// UpdateDevice renames devices.
type UpdateDevice struct {
	devices device.Repository
}

// UpdateDevice satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateDevice)(nil)

// New returns the use case over its dependencies.
func New(devices device.Repository) *UpdateDevice {
	return &UpdateDevice{devices: devices}
}

// Execute renames the appliance.
//
// A revoked device can still be renamed. Its row is what explains a clock entry
// the reader no longer recognizes, and being able to label it "the tablet I
// lost" is the point of keeping it.
func (u *UpdateDevice) Execute(ctx context.Context, input Input) (Output, error) {
	appliance, err := u.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		return Output{}, notTheirs(err)
	}

	// Answered exactly as an identifier nobody has: a reply that distinguished
	// them would tell this reader which devices are somebody else's.
	if !appliance.BelongsTo(input.UserID) {
		return Output{}, notTheirs(nil)
	}

	name, err := device.ParseName(input.Name)
	if err != nil {
		return Output{}, err
	}

	err = appliance.Rename(name)
	if err != nil {
		return Output{}, err
	}

	err = u.devices.Update(ctx, appliance)
	if err != nil {
		return Output{}, err
	}

	return Output{Device: appliance}, nil
}

// notTheirs is the one answer to a device that is not this reader's, whether
// because it does not exist or because it is somebody else's.
func notTheirs(cause error) error {
	return errs.Wrap(cause, errs.KindNotFound, "that device is not bound to this account").
		WithOp(opExecute).
		WithCode(device.CodeNotFound)
}
