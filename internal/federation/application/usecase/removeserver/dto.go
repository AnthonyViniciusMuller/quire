package removeserver

import "uuid"

// Input is a node the reader wants the instance to forget.
type Input struct {
	// ServerID is the row to remove.
	ServerID uuid.UUID
}

// Output is empty: what the call reports is that the node is gone.
type Output struct{}
