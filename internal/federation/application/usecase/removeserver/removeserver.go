// Package removeserver is the delete half of UC12 (RF13).
//
// Two things are never forgotten. This instance, because every reader hosted
// here references its row. And a node any reader still authorizes to hold a
// copy of their data, because forgetting it would leave that reader unable to
// revoke a peer that still has their data — and RN03 is the promise that they
// can.
//
// The second is a check over every reader and not over the caller, since the
// catalogue is node-wide (C15). A reader who wants a node stopped for
// themselves revokes their own authorization, which is the call that is theirs
// alone; this one is about what the instance knows.
//
// It runs as a unit of work, and takes the row lock before it counts. Under
// READ COMMITTED a statement sees the snapshot it began with, so a reader
// authorizing this node between the count and the delete would be invisible to
// both — and the foreign key cascades, so their authorization would go with
// the row rather than stop it. Authorizing takes the same lock, and the two
// then serialize on the node they disagree about.
package removeserver

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "federation/removeserver: execute"

// RemoveServer forgets nodes.
type RemoveServer struct {
	servers     server.Repository
	replicas    replica.Repository
	transaction service.Transaction
}

// RemoveServer satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RemoveServer)(nil)

// New returns the use case over its dependencies.
func New(
	servers server.Repository,
	replicas replica.Repository,
	transaction service.Transaction,
) *RemoveServer {
	return &RemoveServer{servers: servers, replicas: replicas, transaction: transaction}
}

// Execute forgets the node.
//
// It reads, checks and then deletes, and the delete carries both checks again
// in its own statement. The reads are for the answer: they are what lets the
// reader be told which of the two rules refused them, which a statement that
// merely affected no rows could not say. The statement is for the outcome: a
// check the caller ran a moment earlier is a check something could have
// invalidated since, and the foreign key cascades — a delete that got past a
// stale check would take a reader's authorization with it rather than being
// refused.
//
// So a delete that reports no row after the checks passed is a race that was
// lost — which the lock makes unreachable through this node's own calls, and
// the statement still refuses.
func (r *RemoveServer) Execute(ctx context.Context, input Input) (Output, error) {
	return Output{}, r.transaction.Within(ctx, func(ctx context.Context) error {
		node, err := r.servers.GetByIDForUpdate(ctx, input.ServerID)
		if err != nil {
			return err
		}

		if removable := node.Removable(); removable != nil {
			return removable
		}

		authorized, err := r.replicas.CountActiveForServer(ctx, node.ID)
		if err != nil {
			return err
		}

		if authorized > 0 {
			return r.inUse()
		}

		removed, err := r.servers.Delete(ctx, node.ID)
		if err != nil {
			return err
		}

		if !removed {
			return r.inUse()
		}

		return nil
	})
}

// inUse is the answer to a node somebody still allows to hold their data.
func (r *RemoveServer) inUse() error {
	return errs.New(errs.KindFailedPrecondition, "that node still holds data on somebody's behalf").
		WithOp(opExecute).
		WithCode(server.CodeServerInUse).
		WithField("server_id", "a reader here still authorizes it, and forgetting it would leave them "+
			"unable to revoke a peer that has their data")
}
