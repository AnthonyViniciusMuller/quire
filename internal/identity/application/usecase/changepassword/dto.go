package changepassword

import "uuid"

// Input is a reader replacing their own password.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// CurrentPassword proves the session belongs to the reader and not merely
	// to a device somebody left unlocked. It is the check a field mask cannot
	// express, which is why changing a password is its own call rather than an
	// update of the record.
	CurrentPassword string
	// NewPassword is the plaintext, hashed and then dropped.
	NewPassword string
}

// Output is empty. Every session has ended, including this device's, and there
// is nothing to hand back that would still work.
type Output struct{}
