package revokereplica

import "uuid"

// Input is a reader withdrawing a node's permission.
type Input struct {
	// UserID is the reader whose decision this is, taken from the token.
	UserID uuid.UUID
	// ServerID is the node that may no longer hold a copy.
	ServerID uuid.UUID
}

// Output is empty: what the call reports is that the permission is withdrawn.
type Output struct{}
