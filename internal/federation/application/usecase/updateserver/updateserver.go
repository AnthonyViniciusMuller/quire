// Package updateserver is the update half of UC12 that a reader types (RF13).
//
// Only whether the node takes part is editable. Everything else in a catalogue
// row was learned from the node itself, and re-learning it is what
// refreshserver is for — a reader who could type a base URL or a pin by hand
// could also type the wrong one.
//
// Deactivating is guarded the way forgetting is, and for the same reason.
// federation.servers.active is node-wide, like the rest of the catalogue
// (C15), so clearing it stops the replication of every reader who authorized
// that node and not only of the one who asked. A node somebody still
// authorizes is therefore neither deactivable nor removable, and the reader
// who wants it stopped for themselves revokes their own authorization, which
// is the call that is theirs alone.
//
// Clearing the flag runs as a unit of work that takes the row lock first, for
// the reason forgetting a node does: the check reads the authorizations and
// the write touches the catalogue, and a reader authorizing the node between
// the two would be invisible to the check.
package updateserver

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "federation/updateserver: execute"

// UpdateServer writes whether a node takes part.
type UpdateServer struct {
	servers     server.Repository
	replicas    replica.Repository
	transaction service.Transaction
}

// UpdateServer satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateServer)(nil)

// New returns the use case over its dependencies.
func New(
	servers server.Repository,
	replicas replica.Repository,
	transaction service.Transaction,
) *UpdateServer {
	return &UpdateServer{servers: servers, replicas: replicas, transaction: transaction}
}

// Execute writes the flag.
func (u *UpdateServer) Execute(ctx context.Context, input Input) (Output, error) {
	var written *server.Server

	err := u.transaction.Within(ctx, func(ctx context.Context) error {
		node, err := u.servers.GetByIDForUpdate(ctx, input.ServerID)
		if err != nil {
			return err
		}

		// Only the clearing is guarded. Restoring a node nobody is replicating
		// to costs nothing, and restoring one somebody is replicating to is
		// what they wanted.
		if !input.Active && node.Active {
			if refused := u.refuseWhileAuthorized(ctx, node.ID); refused != nil {
				return refused
			}
		}

		// The domain refuses this instance, whatever the flag was going to
		// become: a node that stopped replicating on its own behalf would have
		// taken its own readers out of the federation.
		if err := node.SetActive(input.Active); err != nil {
			return err
		}

		if err := u.servers.Update(ctx, node); err != nil {
			return err
		}

		written = node

		return nil
	})
	if err != nil {
		return Output{}, err
	}

	return Output{Server: written}, nil
}

// refuseWhileAuthorized reports why the node may not be stopped, or nil.
func (u *UpdateServer) refuseWhileAuthorized(ctx context.Context, serverID uuid.UUID) error {
	authorized, err := u.replicas.CountActiveForServer(ctx, serverID)
	if err != nil {
		return err
	}

	if authorized == 0 {
		return nil
	}

	return errs.New(errs.KindFailedPrecondition, "that node still holds data on somebody's behalf").
		WithOp(opExecute).
		WithCode(server.CodeServerInUse).
		WithField("active", "a reader here still authorizes it, and stopping it is not one reader's to do; "+
			"revoke your own authorization instead")
}
