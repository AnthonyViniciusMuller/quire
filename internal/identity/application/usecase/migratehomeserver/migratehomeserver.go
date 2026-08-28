// Package migratehomeserver is UC16: a reader arrives on this node from
// another origin server, bringing their devices with them (RF17).
//
// It is registration with two differences, and both of them are C11 in
// docs/tcc-corrections.md.
//
// The devices travel with their identifiers. Every operation in the reader's
// history names the device that authored it and every vector clock is keyed by
// one, so a node that minted new identifiers would import a history naming
// devices that do not exist there — which sync.operations refuses outright —
// and, even if it did not, a device writing under a new identifier would start
// a second clock entry that never merges with the first. Two devices that had
// been in sync would read as concurrent for ever. So the migration is not
// "register, then push": the devices are adopted, identifiers included, before
// the first operation can be inserted, and a migration carrying none is refused
// rather than accepted into a history nobody could continue.
//
// The previous identity is recorded and never believed. A node that needs
// nothing from the previous server — which is what makes this call independent
// of that server's availability, and the reason UC16 exists — has nothing with
// which to check that the caller was @anthony:old.example. What UC16 preserves
// is the data and not the name: the domain half of the identifier is this node,
// so the identifier changes, and the new one is a new identity that happens to
// hold the old one's history. Peers that replicated the reader hold an
// authorization naming the old identity (RF16), so they are authorized again
// rather than following the reader on their own.
//
// It is a call anybody may make, like registering, and it creates an account
// the same way. What it is not is a way to claim somebody else's: nothing here
// consults the previous identifier, so naming one that belongs to a real reader
// elsewhere buys exactly what naming a fictional one buys, which is a row.
package migratehomeserver

import (
	"context"
	"errors"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "identity/migrate: execute"

// The stable machine-readable codes this use case raises.
const (
	// CodeNoDevices is a migration carrying no device identities, which is a
	// history nobody could continue.
	CodeNoDevices = "migration_carries_no_devices"
	// CodeDeviceIdentifierTaken is a device identifier already bound here, to
	// this reader or to anybody else.
	CodeDeviceIdentifierTaken = "device_identifier_taken"
)

// MigrateHomeServer takes a reader in.
type MigrateHomeServer struct {
	users       user.Repository
	devices     device.Repository
	credentials credential.Repository
	hasher      service.HashService
	auth        service.AuthService
	localServer service.LocalServer
	clock       service.Clock
	transaction service.Transaction
}

// MigrateHomeServer satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*MigrateHomeServer)(nil)

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	devices device.Repository,
	credentials credential.Repository,
	hasher service.HashService,
	auth service.AuthService,
	localServer service.LocalServer,
	clock service.Clock,
	transaction service.Transaction,
) *MigrateHomeServer {
	return &MigrateHomeServer{
		users:       users,
		devices:     devices,
		credentials: credentials,
		hasher:      hasher,
		auth:        auth,
		localServer: localServer,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute creates the account, adopts the devices and issues the session.
//
// All three are one unit of work, and this is the call where that matters most.
// An account created without its devices is an account whose history cannot be
// imported at all — every operation references a device row — and the reader
// would have to be deleted and remade rather than repaired, because the local
// name they chose is now taken by the half-built record.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (m *MigrateHomeServer) Execute(ctx context.Context, input Input) (Output, error) {
	arriving, err := parse(&input)
	if err != nil {
		return Output{}, err
	}

	originServerID, err := m.localServer.ID(ctx)
	if err != nil {
		return Output{}, err
	}

	passwordHash, err := m.hasher.Hash(string(arriving.password))
	if err != nil {
		return Output{}, err
	}

	var output Output

	err = m.transaction.Within(ctx, func(ctx context.Context) error {
		reader, createErr := m.create(ctx, originServerID, arriving, passwordHash)
		if createErr != nil {
			return createErr
		}

		adopted, adoptErr := m.adopt(ctx, reader, arriving.devices)
		if adoptErr != nil {
			return adoptErr
		}

		// The first device is the one making the call. The contract carries no
		// field saying which it is, and the order is what a client controls;
		// C20 in docs/tcc-corrections.md is the finding.
		session, issueErr := m.issue(ctx, reader, adopted[0])
		if issueErr != nil {
			return issueErr
		}

		federatedID, idErr := reader.FederatedID(m.localServer.Domain())
		if idErr != nil {
			return idErr
		}

		output = Output{
			User:        reader,
			FederatedID: federatedID,
			Devices:     adopted,
			Session:     session,
		}

		return nil
	})
	if err != nil {
		return Output{}, err
	}

	return output, nil
}

// create opens the account.
func (m *MigrateHomeServer) create(
	ctx context.Context, originServerID uuid.UUID, arriving *parsedInput, passwordHash string,
) (*user.User, error) {
	now := m.clock.Now()

	reader, err := user.New(&user.Props{
		OriginServerID: originServerID,
		LocalName:      arriving.localName,
		DisplayName:    arriving.displayName,
		Email:          arriving.email,
		PasswordHash:   passwordHash,
		MigratedFrom:   arriving.previous,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		return nil, err
	}

	if created := m.users.Create(ctx, reader); created != nil {
		return nil, created
	}

	return reader, nil
}

// adopt writes the device rows, keeping the identifiers they arrived with.
//
// A device this node already holds is refused, and the refusal is the same
// whether the row belongs to the arriving reader or to somebody else. The
// identifier is what a clock entry is keyed by, so two readers sharing one
// would make two histories indistinguishable — and telling the caller which of
// the two it collided with would be an oracle for the identifiers this node
// holds.
func (m *MigrateHomeServer) adopt(
	ctx context.Context, reader *user.User, arriving []arrival,
) ([]*device.Device, error) {
	adopted := make([]*device.Device, 0, len(arriving))

	for _, appliance := range arriving {
		record := device.Restore(appliance.id, &device.Props{
			UserID:   reader.ID,
			Name:     appliance.name,
			Platform: appliance.platform,
			Active:   true,
		})

		if err := m.devices.Create(ctx, record); err != nil {
			if errors.Is(err, errs.KindAlreadyExists) {
				return nil, taken()
			}

			return nil, err
		}

		adopted = append(adopted, record)
	}

	return adopted, nil
}

// issue signs the access token and stores the credential that outlives it.
func (m *MigrateHomeServer) issue(
	ctx context.Context, reader *user.User, calling *device.Device,
) (service.Session, error) {
	now := m.clock.Now()

	accessToken, claims, err := m.auth.IssueAccess(reader.ID, calling.ID, now)
	if err != nil {
		return service.Session{}, err
	}

	refresh, err := m.auth.IssueRefresh(now)
	if err != nil {
		return service.Session{}, err
	}

	issued, err := credential.NewSession(reader.ID, calling.ID, refresh.Digest, refresh.ExpiresAt)
	if err != nil {
		return service.Session{}, err
	}

	if stored := m.credentials.Create(ctx, issued); stored != nil {
		return service.Session{}, stored
	}

	return service.Session{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  claims.ExpiresAt,
		RefreshToken:          refresh.Value,
		RefreshTokenExpiresAt: refresh.ExpiresAt,
	}, nil
}

// arrival is one device once its fields have become the value objects that
// carry their rules.
type arrival struct {
	id       uuid.UUID
	name     device.Name
	platform device.Platform
}

// parsedInput is what the request looks like once every field has become the
// value object that carries its rule.
type parsedInput struct {
	localName   user.LocalName
	displayName user.DisplayName
	email       user.Email
	password    user.Password
	previous    user.Provenance
	devices     []arrival
}

// parse turns the request into value objects, rejecting the first field that
// breaks its rule.
//
// All of it happens before the password is hashed, as it does in registration
// and for the same reason: hashing is the most expensive thing this call does
// and an unauthenticated caller can ask for it repeatedly.
func parse(input *Input) (*parsedInput, error) {
	localName, err := user.ParseLocalName(input.LocalName)
	if err != nil {
		return nil, err
	}

	displayName, err := user.ParseDisplayName(input.DisplayName)
	if err != nil {
		return nil, err
	}

	email, err := user.ParseEmail(input.Email)
	if err != nil {
		return nil, err
	}

	password := user.Password(input.Password)
	if invalid := password.Validate(); invalid != nil {
		return nil, invalid
	}

	previous, err := user.ParseProvenance(input.PreviousFederatedID)
	if err != nil {
		return nil, err
	}

	devices, err := parseDevices(input.Devices)
	if err != nil {
		return nil, err
	}

	return &parsedInput{
		localName:   localName,
		displayName: displayName,
		email:       email,
		password:    password,
		previous:    previous,
		devices:     devices,
	}, nil
}

// parseDevices reads the appliances the reader is bringing.
//
// A migration carrying none is refused. C11 is the argument and it is not a
// formality: the history the reader is about to import names devices, so a
// migration with no device identities is an account this node can create and
// can never be given anything to hold. Refusing it here is what turns that into
// an answer rather than into a push that fails for ever afterwards.
//
// A device sent twice is not an error to the contract, and it is not one here
// either: the second is dropped, because adopting it twice would be one row and
// two claims on the same identifier.
func parseDevices(arriving []Arrival) ([]arrival, error) {
	if len(arriving) == 0 {
		return nil, errs.New(errs.KindInvalidArgument, "the migration carries no devices").
			WithOp(opExecute).
			WithCode(CodeNoDevices).
			WithField("devices", "a reader's history names the devices that wrote it, "+
				"so at least one has to arrive with its own identifier")
	}

	devices := make([]arrival, 0, len(arriving))
	seen := make(map[uuid.UUID]struct{}, len(arriving))

	for index := range arriving {
		appliance, err := parseDevice(&arriving[index])
		if err != nil {
			return nil, err
		}

		if _, repeated := seen[appliance.id]; repeated {
			continue
		}

		seen[appliance.id] = struct{}{}

		devices = append(devices, appliance)
	}

	return devices, nil
}

// parseDevice reads one appliance.
func parseDevice(arriving *Arrival) (arrival, error) {
	id, err := uuid.Parse(arriving.ID)
	if err != nil {
		return arrival{}, errs.Wrap(err, errs.KindInvalidArgument,
			"a device arrived without the identifier it already holds").
			WithOp(opExecute).
			WithCode(device.CodeInvalidDevice).
			WithField("devices.id", "it must be the uuid the device has been writing under")
	}

	name, err := device.ParseName(arriving.Name)
	if err != nil {
		return arrival{}, err
	}

	platform, err := device.ParsePlatform(arriving.Platform)
	if err != nil {
		return arrival{}, err
	}

	return arrival{id: id, name: name, platform: platform}, nil
}

// taken is the answer to a device identifier this node already holds.
func taken() error {
	return errs.New(errs.KindAlreadyExists, "one of those devices is already bound here").
		WithOp(opExecute).
		WithCode(CodeDeviceIdentifierTaken).
		WithField("devices.id", "a device identifier is what a vector clock entry is keyed by, "+
			"and this node cannot hold two devices under one")
}
