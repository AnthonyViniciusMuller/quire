package service

import (
	"context"
	"uuid"
)

// Replicas answers the two questions a peer-facing call asks, and nothing else.
//
// Which node is calling, and may it hold this reader's data. Both are facts
// about federation.servers and federation.user_replicas, which this slice does
// not own — so it names what it needs, and an adapter in
// internal/sync/infra/service satisfies it out of the federation slice's own
// repositories. That is the pattern the reading slice's Works port set, and the
// reason is the same: the coupling is two methods rather than an import of
// another slice's domain in every use case.
//
// The second question is the only place in this contract where a call is
// refused on the reader's own instruction. RN03 is why: a replica holds a copy
// of somebody's library, and holding it is something the reader grants and
// revokes (RF16, UC15).
type Replicas interface {
	// Identify returns the node in the catalogue that published the pin, and
	// an error of kind errs.KindPermissionDenied when no active node did.
	//
	// A pin nobody published is answered exactly as a pin belonging to a node
	// the operator has stopped, because the two are one fact to whoever
	// presented it: this node is not replicating with you.
	Identify(ctx context.Context, pin string) (uuid.UUID, error)

	// Authorized reports nil when the reader lets the node send their changes
	// here: the reader authorized it, and it is the reader's origin.
	//
	// The second half is what makes replication one way. A reader's origin
	// is the one node that authenticates them, so it is the one node whose
	// word about which device authored a change is worth anything; a node
	// that accepted a reader's changes from any node the reader authorized
	// would let a replica write into the reader's history under any of their
	// devices, and holding a copy would have become the authority to write.
	// It is errs.KindPermissionDenied otherwise, and the refusal does not say
	// which half failed.
	Authorized(ctx context.Context, serverID, userID uuid.UUID) error
}
