package service

import (
	"context"
	"uuid"
)

// Admissions is the origin's half of C22 as the delivery pass sees it: before
// a reader's changes are offered to a node, the node is told that it holds
// them.
//
// It is here rather than in the use case that records the reader's
// authorization, because admission is a standing obligation and not a
// handshake. A replica has to hold the reader and every device that authored
// anything of theirs, and devices are bound after the authorization as often
// as before it; a call made once, at the authorization, would leave every
// later device unknown to the replica and every change it authors refused.
// The pass that delivers the changes is the one place that knows, every time,
// that a reader's changes are about to reach a node — so it is the place that
// makes sure the node can hold them.
type Admissions interface {
	// Admit makes sure the node holds the reader and their devices as this
	// node knows them now. It is cheap to call again: an adapter remembers
	// what a node was last told and calls the node only when that changed,
	// or when the last call failed.
	//
	// A node that could not be reached is errs.KindUnavailable; a node that
	// refused is errs.KindFailedPrecondition with its reason.
	Admit(ctx context.Context, serverID, userID uuid.UUID) error
}
