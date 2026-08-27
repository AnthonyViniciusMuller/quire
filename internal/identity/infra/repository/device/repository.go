// Package device is the PostgreSQL adapter of the device repository: it
// satisfies the port declared in internal/identity/domain/device and is the
// only place that knows identity.devices exists.
package device

import (
	"context"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate     = "identity/device: create"
	opUpdate     = "identity/device: update"
	opGetByID    = "identity/device: get by id"
	opListByUser = "identity/device: list by user"
)

// Repository reads and writes devices in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ device.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *identitydb.Queries {
	return identitydb.New(r.manager.Executor(ctx))
}

// Create binds the device.
func (r *Repository) Create(ctx context.Context, appliance *device.Device) error {
	err := r.queries(ctx).CreateDevice(ctx, identitydb.CreateDeviceParams{
		ID:           appliance.ID,
		UserID:       appliance.UserID,
		Name:         appliance.Name.String(),
		Platform:     appliance.Platform.String(),
		LastSyncedAt: optionalTime(appliance.LastSyncedAt),
		Active:       appliance.Active,
	})

	return persist.Classify(err, opCreate)
}

// Update writes back the three mutable columns.
func (r *Repository) Update(ctx context.Context, appliance *device.Device) error {
	rows, err := r.queries(ctx).UpdateDevice(ctx, identitydb.UpdateDeviceParams{
		ID:           appliance.ID,
		Name:         appliance.Name.String(),
		LastSyncedAt: optionalTime(appliance.LastSyncedAt),
		Active:       appliance.Active,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByID reads a device by primary key, bound or not.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*device.Device, error) {
	row, err := r.queries(ctx).GetDeviceByID(ctx, id)
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err, opGetByID)
		}

		return nil, persist.Classify(err, opGetByID)
	}

	return toDomain(&row), nil
}

// ListByUser reads the devices of a reader.
func (r *Repository) ListByUser(
	ctx context.Context,
	userID uuid.UUID,
	includeInactive bool,
) ([]*device.Device, error) {
	rows, err := r.queries(ctx).ListDevicesByUser(ctx, identitydb.ListDevicesByUserParams{
		UserID:          userID,
		IncludeInactive: includeInactive,
	})
	if err != nil {
		return nil, persist.Classify(err, opListByUser)
	}

	devices := make([]*device.Device, 0, len(rows))
	for index := range rows {
		devices = append(devices, toDomain(&rows[index]))
	}

	return devices, nil
}

// notFound is the answer to a device that is not here.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no such device").
		WithOp(op).
		WithCode(device.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the id is the one every clock entry of this appliance is keyed
// by, and minting a new one would make its whole history unattributable.
func toDomain(row *identitydb.IdentityDevice) *device.Device {
	props := device.Props{
		UserID:   row.UserID,
		Name:     device.Name(row.Name),
		Platform: device.Platform(row.Platform),
		Active:   row.Active,
	}

	// Null until the first completed synchronization, which the domain reads as
	// the zero instant.
	if row.LastSyncedAt != nil {
		props.LastSyncedAt = *row.LastSyncedAt
	}

	return device.Restore(row.ID, &props)
}

// optionalTime renders the zero instant as the NULL the column holds for a
// device that has never synchronized.
func optionalTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}

	return &at
}
