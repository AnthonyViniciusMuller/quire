// Package watchoperations is UC10 for a caller that cannot hold Sync open.
//
// It reads one number: this node's head position for a reader. What makes that
// a use case rather than a field read is where the number comes from — the log,
// through the same port every other use case of this slice reads it through —
// and what the controller above it does with it, which is to stay open and
// report the number whenever it changes.
//
// There is nothing here about streams, waking or coalescing. That belongs to
// the controller, in the same way that the Sync stream's window belongs to its
// controller and not to the push and pull use cases it composes.
package watchoperations

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// WatchOperations reads how far a reader's log has got.
type WatchOperations struct {
	log operation.Repository
}

// WatchOperations satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*WatchOperations)(nil)

// New returns the use case over the log.
func New(log operation.Repository) *WatchOperations {
	return &WatchOperations{log: log}
}

// Execute reports this node's last allocated position for the reader, and zero
// for a reader whose log is empty.
//
// It asks nothing about the reader beyond the identifier it was given, for the
// reason the pull use case does not: the log is scoped to one reader by the
// column every statement filters on, and who that reader is was settled by the
// controller from the session.
func (w *WatchOperations) Execute(ctx context.Context, input Input) (Output, error) {
	head, err := w.log.Head(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	return Output{LastPosition: head}, nil
}
