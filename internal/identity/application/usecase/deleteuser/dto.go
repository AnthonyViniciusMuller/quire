package deleteuser

import "uuid"

// Input is a reader removing themselves from this node.
type Input struct {
	// UserID is the reader the call is made on behalf of.
	UserID uuid.UUID
	// Password proves the session belongs to the reader, for the reason
	// ChangePassword asks for it — and with more at stake, since what this call
	// does cannot be undone.
	Password string
}

// Output is empty. There is nothing left to describe.
type Output struct{}
