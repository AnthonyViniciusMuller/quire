package withdrawreplica

import "uuid"

// Input is the reader whose permission the origin says is gone, and the pin
// the origin said it with.
type Input struct {
	// Pin is the public key digest of the certificate the caller presented.
	Pin string

	// UserID is the reader who withdrew the permission.
	UserID uuid.UUID
}

// Output is empty.
type Output struct{}
