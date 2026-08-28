// Package pulloperations is UC09 outbound and the whole of RN06: everything
// after the cursor, in the order this node committed it.
//
// The cursor is a position and not a timestamp, and C08 in
// docs/tcc-corrections.md is the argument: an operation stamped early and
// committed late is skipped by a cursor that has already moved past it, and it
// is not delayed, it is lost. The position is allocated inside the writing
// transaction from a row lock held until commit, so the order of the numbers is
// the order of the commits — which is what makes a single number a sufficient
// cursor, since a caller that has seen position N has necessarily seen every
// position below it.
//
// The page includes the caller's own changes. A device pulling gets back what
// it pushed, which costs one comparison there and is what keeps the cursor
// meaning "everything this node holds below here": a page that hid the caller's
// own operations would leave gaps in the numbering, and a device could not tell
// a gap it caused from a gap it was not told about.
package pulloperations

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// PullOperations reads a reader's log.
type PullOperations struct {
	log operation.Repository
}

// PullOperations satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*PullOperations)(nil)

// New returns the use case over the log.
func New(log operation.Repository) *PullOperations {
	return &PullOperations{log: log}
}

// Execute reads one page.
//
// It asks nothing about the reader beyond the identifier it was given. The log
// is scoped to one reader by the column every statement filters on, and who
// that reader is was settled by the controller — from the session for a device,
// and from the authorization for a peer.
func (p *PullOperations) Execute(ctx context.Context, input Input) (Output, error) {
	operations, more, err := p.log.List(ctx, &operation.Query{
		UserID:        input.UserID,
		AfterPosition: input.AfterPosition,
		Size:          pageSize(input.Limit),
	})
	if err != nil {
		return Output{}, err
	}

	// An empty page leaves the cursor where the caller had it. Answering with
	// a zero would send a device that has drained the log back to the
	// beginning of it.
	last := input.AfterPosition
	if len(operations) > 0 {
		last = operations[len(operations)-1].Position
	}

	return Output{Operations: operations, LastPosition: last, HasMore: more}, nil
}

// pageSize clamps what the caller asked for.
//
// A caller that asked for nothing gets the default, and one that asked for more
// than the node will assemble gets the ceiling rather than a refusal: the reply
// carries the cursor to continue from, so a smaller page than was asked for
// costs a round trip and never an operation.
func pageSize(limit int) int {
	switch {
	case limit <= 0:
		return operation.DefaultPageSize
	case limit > operation.MaxPageSize:
		return operation.MaxPageSize
	default:
		return limit
	}
}
