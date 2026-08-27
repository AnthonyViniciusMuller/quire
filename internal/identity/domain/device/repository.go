package device

import (
	"context"
	"uuid"
)

// Repository is the port through which the use cases of the identity slice read
// and write devices. It belongs to the domain; what satisfies it lives in
// internal/identity/infra/repository/device.
//
// As in the reader's repository, the context is passed so that a call can join
// the transaction the manager carries, and a device that does not exist is an
// error of kind errs.KindNotFound rather than a zero value.
type Repository interface {
	// Create binds the device.
	Create(ctx context.Context, device *Device) error

	// Update writes back the name, the last synchronization and the binding
	// flag.
	Update(ctx context.Context, device *Device) error

	// GetByID reads a device by primary key, bound or not. Revoked devices are
	// readable on purpose: the reader is shown one when they ask what a clock
	// entry refers to.
	GetByID(ctx context.Context, id uuid.UUID) (*Device, error)

	// ListByUser reads the devices of a reader, ordered by name so that the
	// list RF11 makes auditable does not reshuffle between two calls. Unbound
	// devices are included only when asked for.
	ListByUser(ctx context.Context, userID uuid.UUID, includeInactive bool) ([]*Device, error)
}
