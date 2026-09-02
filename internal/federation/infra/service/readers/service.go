// Package readers writes the readers a peer admits here, through the identity
// slice's own repositories.
//
// It is the adapter of the Readers port the admission use case holds. A
// replica holds a row for every reader it replicates and for every device
// that authored anything of theirs, and both rows belong to the identity
// slice: what a replicated reader is — a name and no password, C03 — is that
// slice's definition, and it is applied here by building the entity the
// identity slice restores rather than by a statement of this slice's own.
// That is the shape internal/reading/infra/service/works set, and it is wired
// the same way, in cmd/quired where the two containers meet.
package readers

import (
	"context"
	"errors"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	identitydevice "github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	identityuser "github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opAdmit is the operation reported by this file, in the form the errs package
// expects.
const opAdmit = "federation/readers: admit"

// The stable machine-readable codes this adapter raises.
const (
	// CodeReaderHeld is a reader this node holds under another node — one it
	// hosts, or one another origin replicates here.
	CodeReaderHeld = "reader_held_by_another_node"
	// CodeDeviceHeld is a device identifier bound to another reader here.
	CodeDeviceHeld = "device_held_by_another_reader"
)

// Service admits readers into the identity slice's tables.
type Service struct {
	users   identityuser.Repository
	devices identitydevice.Repository
	clock   service.Clock
}

// Service satisfies the port the use cases hold.
var _ service.Readers = (*Service)(nil)

// New returns the adapter over the identity slice's repositories.
func New(users identityuser.Repository, devices identitydevice.Repository, clock service.Clock) *Service {
	return &Service{users: users, devices: devices, clock: clock}
}

// Admit records the reader as replicated from the origin and the devices as
// theirs, creating what is missing and leaving what is there.
func (s *Service) Admit(
	ctx context.Context, originServerID uuid.UUID, reader *service.Reader, devices []service.Device,
) error {
	if err := s.admitReader(ctx, originServerID, reader); err != nil {
		return err
	}

	for index := range devices {
		if err := s.admitDevice(ctx, reader.ID, &devices[index]); err != nil {
			return err
		}
	}

	return nil
}

// admitReader creates the reader's row, or checks the one that exists is the
// origin's to claim.
func (s *Service) admitReader(ctx context.Context, originServerID uuid.UUID, reader *service.Reader) error {
	localName, err := identityuser.ParseLocalName(reader.LocalName)
	if err != nil {
		return err
	}

	displayName, err := identityuser.ParseDisplayName(reader.DisplayName)
	if err != nil {
		return err
	}

	held, err := s.users.GetByID(ctx, reader.ID)

	switch {
	case errors.Is(err, errs.KindNotFound):
		now := s.clock.Now()

		// No address and no password, which is what makes this a replicated
		// reader rather than one this node authenticates (C03): the row
		// exists so that what they wrote has somewhere to hang, and RN08
		// leaves authenticating them to the node that hosts them.
		return s.users.Create(ctx, identityuser.Restore(reader.ID, &identityuser.Props{
			OriginServerID: originServerID,
			LocalName:      localName,
			DisplayName:    displayName,
			CreatedAt:      now,
			UpdatedAt:      now,
		}))
	case err != nil:
		return err
	case held.OriginServerID != originServerID:
		return errs.New(errs.KindPermissionDenied, "the reader is held here under another node").
			WithOp(opAdmit).
			WithCode(CodeReaderHeld)
	default:
		return nil
	}
}

// admitDevice creates the device's row, or checks the one that exists is the
// reader's.
//
// The identifier is kept as it arrived, and C11 is why: every operation names
// the device that authored it and every vector clock entry is keyed by it, so
// a replica that minted its own would hold a history about devices it cannot
// find.
func (s *Service) admitDevice(ctx context.Context, readerID uuid.UUID, appliance *service.Device) error {
	name, err := identitydevice.ParseName(appliance.Name)
	if err != nil {
		return err
	}

	platform, err := identitydevice.ParsePlatform(appliance.Platform)
	if err != nil {
		return err
	}

	held, err := s.devices.GetByID(ctx, appliance.ID)

	switch {
	case errors.Is(err, errs.KindNotFound):
		return s.devices.Create(ctx, identitydevice.Restore(appliance.ID, &identitydevice.Props{
			UserID:   readerID,
			Name:     name,
			Platform: platform,
			Active:   true,
		}))
	case err != nil:
		return err
	case !held.BelongsTo(readerID):
		return errs.New(errs.KindPermissionDenied, "the device belongs to another reader").
			WithOp(opAdmit).
			WithCode(CodeDeviceHeld)
	default:
		return nil
	}
}
