package migratehomeserver

import (
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// Arrival is one device the reader is bringing with them.
type Arrival struct {
	// ID is the identifier the device already holds, and the whole reason the
	// list exists (C11). Every operation in the reader's history names it and
	// every vector clock is keyed by it, so a node that minted a new one would
	// import a history naming devices that do not exist there — and a device
	// writing under a new identifier would start a second clock entry that
	// never merges with the first.
	ID string

	// Name is what the reader calls the appliance.
	Name string

	// Platform is the operating system it runs.
	Platform string
}

// Input is a reader arriving from another origin server (RF17, UC16).
type Input struct {
	// LocalName is the half of the identifier the reader chooses. It may be
	// the one they had, if it is free here.
	LocalName string
	// DisplayName is what they call themselves.
	DisplayName string
	// Email is the address on record.
	Email string
	// Password is the plaintext, hashed and then dropped.
	Password string

	// PreviousFederatedID is the identifier they are arriving from, recorded
	// as provenance. This node cannot verify it and must not treat it as
	// authenticated — C11 in docs/tcc-corrections.md.
	PreviousFederatedID string

	// Devices are the appliances to adopt, with the identifiers they already
	// hold. The first is the one making the call, and the one the session
	// comes back for.
	Devices []Arrival
}

// Output is the account as it now exists here, and a session to begin pushing
// with.
type Output struct {
	// User is the reader on this node. It is a new identity that happens to
	// hold the old one's history: the domain half of the identifier is this
	// node, so the identifier changes whatever the local name turned out to be.
	User *user.User

	// FederatedID is the identifier they now have.
	FederatedID user.FederatedID

	// Devices are the appliances as adopted, which a client should compare
	// against what it sent before pushing anything.
	Devices []*device.Device

	// Session is for the calling device, so that it can begin pushing its
	// local log immediately. The other devices log in as they normally would.
	Session service.Session
}
