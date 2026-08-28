package replicateoperations

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Input is a batch of changes one node offers another.
type Input struct {
	// Pin is the public key digest of the certificate the caller presented,
	// which is what identifies the calling node (RNF08).
	//
	// It is the credential and not the identity: what the use case does with it
	// is look it up, and a caller that presented a certificate no node in the
	// catalogue published is refused exactly as one that presented none.
	Pin string

	// UserID is the reader whose log the changes belong to.
	//
	// A device-facing call takes this from the session; this one cannot,
	// because the certificate identifies the calling node and not any of the
	// readers it replicates. It is the reason the peer-facing call has a field
	// the device-facing one does not.
	UserID uuid.UUID

	// Operations are the changes, in the order they were offered.
	Operations []pushoperations.Change
}

// Output is one verdict per change offered.
//
// It carries no head position, unlike a device's push. A peer's cursor into
// this node's log is meaningless to it — the position is node-local, and the
// peer numbers the same operations differently — so telling it where this log
// ends would be a number it could not use.
type Output struct {
	// Results are the verdicts, in the order the changes were offered.
	Results []operation.Result
}
