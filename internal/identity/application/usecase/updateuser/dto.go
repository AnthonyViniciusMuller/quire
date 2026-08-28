package updateuser

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// Input is a reader changing their own record.
//
// The writable field is a pointer because absence is not emptiness: a client
// that is not touching a field and one that means to clear it look identical
// without a way to tell them apart. Nil is "leave it alone", and it is what the
// contract's field mask decodes to. It stays a pointer with one field, because
// what makes it one is the mask and not how many paths the mask may carry.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// DisplayName is the new shown name, or nil.
	DisplayName *string
}

// Output is the record as it now reads.
type Output struct {
	// User is the record that was written.
	User *user.User
	// FederatedID is unchanged by anything here — neither writable field is
	// part of it — and is returned so that the reply is the same shape as the
	// one that was read.
	FederatedID user.FederatedID
}
