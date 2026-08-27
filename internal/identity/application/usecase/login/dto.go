package login

import (
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// Input is what a device presents to start a session.
type Input struct {
	// LocalName is one of the two ways a reader names themselves. Exactly one
	// of it and Email is set; the contract makes that a oneof, so a server
	// never has to guess which was meant by looking for an at sign.
	LocalName string
	// Email is the other.
	Email string
	// Password is the plaintext, checked and then dropped.
	Password string
	// Device is the appliance the session is for. A session always belongs to
	// one, because the credential is revoked with it and because RN10 checks
	// every operation against it.
	Device Binding
}

// Binding is how a device names itself when it asks for a session.
type Binding struct {
	// DeviceID is set on an appliance that is already bound, to go on using the
	// same identity. Empty, a new device is created — and one that forgets its
	// id and omits it starts a second clock entry that never merges with the
	// first.
	DeviceID string
	// Name is what the reader calls this appliance, used only when it is being
	// bound for the first time.
	Name string
	// Platform is the operating system it runs, used at the same moment.
	Platform string
}

// Output is the session, and everything the device needs in order to use it.
type Output struct {
	// Session is the pair of credentials the device presents from now on.
	Session service.Session
	// User is the reader, as the contract returns them to themselves.
	User *user.User
	// Device carries the id the appliance must use in its vector clocks, which
	// is the whole point of the binding for one being bound here for the first
	// time.
	Device *device.Device
}
