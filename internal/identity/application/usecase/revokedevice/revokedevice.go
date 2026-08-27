// Package revokedevice unbinds one of a reader's appliances: it may no longer
// write, and its sessions end.
//
// The record itself survives, and that is the point. Every operation the device
// ever authored is still keyed by its identifier, and a vector clock naming a
// device nobody can resolve cannot be explained to the reader, audited, or
// checked against RN10.
package revokedevice

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/revokedevice: execute"

// RevokeDevice unbinds devices.
type RevokeDevice struct {
	devices     device.Repository
	credentials credential.Repository
	transaction service.Transaction
}

// RevokeDevice satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RevokeDevice)(nil)

// New returns the use case over its dependencies.
func New(
	devices device.Repository,
	credentials credential.Repository,
	transaction service.Transaction,
) *RevokeDevice {
	return &RevokeDevice{devices: devices, credentials: credentials, transaction: transaction}
}

// Execute unbinds the appliance and ends its sessions.
//
// The two are one unit of work. Clearing the flag without revoking the
// credentials would leave the appliance able to refresh — which is exactly what
// Quadro 17 says an inactive device must not do — and revoking the credentials
// without clearing the flag would leave it able to log in again.
func (r *RevokeDevice) Execute(ctx context.Context, input Input) (Output, error) {
	appliance, err := r.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		return Output{}, notTheirs(err)
	}

	if !appliance.BelongsTo(input.UserID) {
		return Output{}, notTheirs(nil)
	}

	// Already unbound. The caller asked that this device stop writing, and it
	// has, so there is nothing to do and nothing to report — the same reasoning
	// that makes logging out idempotent.
	if !appliance.Active {
		return Output{}, nil
	}

	appliance.Revoke()

	err = r.transaction.Within(ctx, func(ctx context.Context) error {
		if updateErr := r.devices.Update(ctx, appliance); updateErr != nil {
			return updateErr
		}

		return r.credentials.ConsumeForDevice(ctx, appliance.ID)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}

// notTheirs is the one answer to a device that is not this reader's.
func notTheirs(cause error) error {
	return errs.Wrap(cause, errs.KindNotFound, "that device is not bound to this account").
		WithOp(opExecute).
		WithCode(device.CodeNotFound)
}
