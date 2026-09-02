package service

import (
	"context"
	"uuid"
)

// Admission is what an origin tells a replica about a reader: who they are,
// the devices that author their changes, and how much the reader allowed.
type Admission struct {
	Reader          Reader
	Devices         []Device
	ReplicatesFiles bool
}

// Peers is the calls this node makes to other nodes on a reader's behalf: the
// two halves of C22, by which an origin tells a replica that it is one, and
// that it has stopped being one.
//
// It is a port because a peer is reached over a connection this slice does
// not open — the shared dialer of internal/shared/grpcx, which the sync slice
// offers changes over as well — and because a use case that dialed would be a
// use case tested against a listener.
type Peers interface {
	// Admit tells the node that the reader has authorized it, carrying what
	// it has to store. It may be called again for a reader already admitted;
	// the node adds what is new and changes nothing else.
	//
	// A node that could not be reached is errs.KindUnavailable. A node that
	// refused is errs.KindFailedPrecondition with the node's own reason,
	// because the reason is the one thing an operator can act on.
	Admit(ctx context.Context, serverID uuid.UUID, admission *Admission) error

	// Withdraw tells the node that the reader has withdrawn the permission.
	Withdraw(ctx context.Context, serverID, userID uuid.UUID) error
}
