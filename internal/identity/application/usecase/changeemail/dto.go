package changeemail

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// Input is a reader replacing the address their account is recovered through.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// Password is the current password, which is what proves the reader is
	// present rather than merely a device of theirs being unlocked.
	Password string
	// Email is the new address.
	Email string
}

// Output is the record as it now reads.
type Output struct {
	// User is the record that was written.
	User *user.User
	// FederatedID is unchanged — the address is no part of it — and is returned
	// so that the reply is the same shape as the one UpdateUser gives.
	FederatedID user.FederatedID
}
