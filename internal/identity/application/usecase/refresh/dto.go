package refresh

import "github.com/anthonyvsmuller/quire/internal/identity/application/service"

// Input is the credential the device presents in order to keep its session.
//
// It is the only thing the call takes. The access token is short-lived by RNF11
// and is routinely expired by the time a device needs this, so requiring one
// would make the shorter lifetime the shorter session.
type Input struct {
	// RefreshToken is the credential, as its holder has it.
	RefreshToken string
}

// Output is the replacement session.
//
// Both halves are new. The credential presented is spent, so a device that kept
// the old one and used the new one holds a value that will end its sessions the
// next time it is presented — which is the point of D07 in
// docs/tcc-corrections.md.
type Output struct {
	// Session is the pair of credentials the device presents from now on.
	Session service.Session
}
