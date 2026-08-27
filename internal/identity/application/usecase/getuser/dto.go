package getuser

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// Input is a reader asking for their own record.
type Input struct {
	// UserID is the reader the call is made on behalf of, taken from the
	// session. There is no other parameter, and there is not meant to be: a
	// node never serves one reader's record to another.
	UserID uuid.UUID
}

// Output is the reader as their own node knows them.
type Output struct {
	// User is the record, address included — which is what makes this the only
	// reply that may carry one (RN09, C03).
	User *user.User
	// FederatedID is @local_name:domain, assembled so that every client renders
	// the identifier the same way.
	FederatedID user.FederatedID
}
