package register

import "github.com/anthonyvsmuller/quire/internal/identity/domain/user"

// Input is what a reader supplies to open an account on this node.
//
// The fields are strings and not the value objects of the domain: this is the
// edge, and what arrives here has been through nothing but a protobuf decoder.
// Turning them into value objects is the first thing Execute does, and it is
// where a malformed one is rejected with the name of the field it came from.
//
// The server half of the identifier is deliberately not a parameter. UC14 binds
// the reader to the node the call was addressed to, so which node that is
// cannot be something the caller chooses.
type Input struct {
	// LocalName is the identifier the reader asked for.
	LocalName string
	// DisplayName is what they call themselves.
	DisplayName string
	// Email is the address the recovery of UC08 will be sent to.
	Email string
	// Password is the plaintext, which is hashed and then dropped. It never
	// reaches a log line, an error message or a row.
	Password string
}

// Output is the reader as this node now knows them.
type Output struct {
	// User is the record that was written.
	User *user.User
	// FederatedID is @local_name:domain, assembled from the record and the
	// node's own domain so that every client renders it the same way.
	FederatedID user.FederatedID
}
