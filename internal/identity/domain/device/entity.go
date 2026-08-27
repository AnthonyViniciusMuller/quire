// Package device is the appliance a reader writes from: the entity, the value
// objects that describe it, and the port a repository has to satisfy.
//
// A device is an identity and not a label. Every synchronization operation
// names the device that authored it and every vector clock entry is keyed by a
// device id, so an appliance that was never bound would introduce an entry no
// node could attribute to anybody, and RN10 — an operation is accepted only
// when the authenticated device is the one the operation declares — would have
// nothing to check against.
//
// That is also why unbinding clears a flag rather than deleting the row: the
// operations the device authored keep naming it, and a clock entry pointing at
// a device nobody can resolve cannot be explained to the reader.
package device

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by the constructor.
const opNew = "identity/device: new"

// CodeInvalidDevice is the code carried by a device that could not be built for
// a reason none of the value objects owns.
const CodeInvalidDevice = "invalid_device"

// Props is everything about a device other than its identifier.
type Props struct {
	// UserID is the reader the device writes on behalf of.
	UserID uuid.UUID
	// Name is what the reader calls this appliance.
	Name Name
	// Platform is the operating system it runs. The schema leaves it
	// free-form, so that the set of platforms a reader may use is not something
	// a node has to be redeployed to extend.
	Platform Platform
	// LastSyncedAt is the instant of the last completed synchronization, zero
	// until the first one.
	//
	// It is a diagnostic and not a cursor. C08 in docs/tcc-corrections.md is
	// the argument: a timestamp assigned when an operation is written cannot
	// order operations by when they became visible, so what decides which
	// operations a device still needs is its position in the stream. This
	// column only tells a reader when their tablet last spoke to the node.
	LastSyncedAt time.Time
	// Active is false once the device has been unbound. The row stays.
	Active bool
}

// Device is one appliance bound to a reader's account (RF11, UC10).
type Device struct {
	// ID is the identity a vector clock entry is keyed by. It outlives the
	// binding, and an appliance that forgets it and asks to be bound again
	// starts a second entry that never merges with the first.
	ID uuid.UUID

	Props
}

// New binds an appliance to a reader. It is bound the moment it is created,
// which is what a device asking for a session is asking for.
func New(userID uuid.UUID, name Name, platform Platform) (*Device, error) {
	if err := name.Validate(); err != nil {
		return nil, err
	}

	if err := platform.Validate(); err != nil {
		return nil, err
	}

	if userID == (uuid.UUID{}) {
		return nil, errs.New(errs.KindInvalidArgument, "the device has no owner").
			WithOp(opNew).
			WithCode(CodeInvalidDevice).
			WithField("user_id", "it must name the reader the device writes for")
	}

	return &Device{
		// Minted here for the same reason a reader's is: the caller holds the
		// id before the insert, and a login that binds a device has to put that
		// id in the reply and in the session it issues.
		ID: uuid.New(),
		Props: Props{
			UserID:   userID,
			Name:     name,
			Platform: platform,
			Active:   true,
		},
	}, nil
}

// Restore rebuilds a device already stored, without minting an identifier: the
// id is the one every clock entry of this appliance is keyed by, and a
// repository that replaced it would make the whole history unattributable.
func Restore(id uuid.UUID, props *Props) *Device {
	return &Device{ID: id, Props: *props}
}

// Rename records a new name. Nothing else about a bound device is editable: its
// platform is what it is, and its id is referenced by every clock it appears in.
func (d *Device) Rename(name Name) error {
	if err := name.Validate(); err != nil {
		return err
	}

	d.Name = name

	return nil
}

// Revoke unbinds the device: it may no longer write. The sessions issued to it
// are ended by the caller in the same transaction — this method only says that
// the binding is over.
func (d *Device) Revoke() { d.Active = false }

// MarkSynced records the instant of a completed synchronization.
func (d *Device) MarkSynced(at time.Time) { d.LastSyncedAt = at }

// HasSynced reports whether the device has ever completed one.
func (d *Device) HasSynced() bool { return !d.LastSyncedAt.IsZero() }
