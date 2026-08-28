package service

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Records is the port through which this slice reaches the records that
// operations target.
//
// It is one method, and everything the slice knows about a work, a grouping, a
// filing, a mark or a position is behind it. That is deliberate: the sync slice
// owns the log and owns nothing that is replicated through it, so a port that
// handed back records would invite a use case here to decide something about a
// work — and what a work is remains the library slice's to say.
//
// It is the shape the reading slice's Works port set, one step further along.
// There the question was whether a reader may write in a work; here it is what
// becomes of a change to a record, which is a question about five tables in two
// other slices. The adapter in internal/sync/infra/service/records is what
// knows they exist, and it reaches every one of them through the repository its
// own slice declares.
//
// The decision it applies is not its own. Which of two versions of a record
// survives is crdt.Revision.Supersedes, in the shared core, where the merge
// laws are proved; what the adapter contributes is resolving the record the
// operation names, decoding the fields the delta claims, and writing the
// result. A rejection is a verdict and not an error, because a batch continues
// past one: an error here is the node failing, and it stops the batch.
type Records interface {
	// Reconcile merges one operation into the record it names, and reports
	// what became of it.
	Reconcile(ctx context.Context, op *operation.Operation) (operation.Verdict, error)
}
