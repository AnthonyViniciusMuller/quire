package service

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Peers is the port through which this node offers a reader's changes to
// another node.
//
// It is the only outbound port in the slice, and the only place in the node
// where Quire is a client of Quire. What it hides is the whole of how a peer is
// reached: the catalogue row that says where the node answers, the pin that
// says which certificate is that node's (RNF08, C12), and the connection kept
// open between ticks.
//
// The reader is a parameter rather than something the operations carry, because
// that is what the call is: a peer replicates many readers and the certificate
// identifies the node rather than any one of them, so the reader has to be
// named explicitly and the destination checks it against the authorization the
// reader gave (RN03, RF16).
type Peers interface {
	// Replicate offers a reader's changes to one node and reports what became
	// of each of them there.
	//
	// The results are per operation and are not necessarily one per operation
	// offered: a destination that answered about some of them has answered
	// about some of them, and what it did not answer for is still owed. An
	// error is the call not happening at all, which is the ordinary state of a
	// peer belonging to another operator.
	Replicate(
		ctx context.Context,
		serverID, userID uuid.UUID,
		operations []*operation.Operation,
	) ([]operation.Result, error)
}
