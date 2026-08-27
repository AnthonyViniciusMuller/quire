// Package authorizereplica is the granting half of UC15: it allows a known
// node to hold a copy of the reader's data (RF16).
//
// It is the only thing that lets data leave this node for a peer. RN03 is not
// a policy stated somewhere and enforced elsewhere — it is this row, and the
// replication of phase 9 walks these rows and nothing else.
//
// One row per (reader, node) pair, reused as the decision changes, so that a
// grant and its revocation stay in one place rather than becoming two
// histories of one decision.
package authorizereplica

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "federation/authorizereplica: execute"

// AuthorizeReplica grants permissions.
type AuthorizeReplica struct {
	servers     server.Repository
	replicas    replica.Repository
	clock       service.Clock
	transaction service.Transaction
}

// AuthorizeReplica satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*AuthorizeReplica)(nil)

// New returns the use case over its dependencies.
func New(
	servers server.Repository,
	replicas replica.Repository,
	clock service.Clock,
	transaction service.Transaction,
) *AuthorizeReplica {
	return &AuthorizeReplica{servers: servers, replicas: replicas, clock: clock, transaction: transaction}
}

// Execute grants the permission.
//
// It runs as a unit of work and takes the catalogue row's lock first. That
// lock is what makes the refusals on the other side hold: forgetting a node
// and stopping one both read these authorizations and then write the
// catalogue, and a grant committed between their check and their write would
// be invisible to the check and taken by the cascade. The three calls
// serialize on the node they disagree about.
func (a *AuthorizeReplica) Execute(ctx context.Context, input Input) (Output, error) {
	var (
		granted *replica.Replica
		domain  server.Domain
	)

	err := a.transaction.Within(ctx, func(ctx context.Context) error {
		node, err := a.servers.GetByIDForUpdate(ctx, input.ServerID)
		if err != nil {
			return err
		}

		if refused := a.refuseUnsuitable(node); refused != nil {
			return refused
		}

		domain = node.Domain
		granted, err = a.grant(ctx, input)

		return err
	})
	if err != nil {
		return Output{}, err
	}

	return Output{Authorization: granted, ServerDomain: domain}, nil
}

// refuseUnsuitable reports why the node may not hold a copy, or nil.
func (a *AuthorizeReplica) refuseUnsuitable(node *server.Server) error {
	refuse := func(code, reason string) error {
		return errs.New(errs.KindFailedPrecondition, "that node cannot hold a copy of this reader's data").
			WithOp(opExecute).
			WithCode(code).
			WithField("server_id", reason)
	}

	switch {
	case node.IsLocal:
		// A reader hosted here does not authorize a replica of themselves on
		// the node they already live on. The row would say nothing, and it
		// would then be a row refusing to let this instance be deactivated.
		return refuse(server.CodeLocalServer, "it is this node, which already holds the reader's data")
	case !node.Active:
		// The replication worker walks the nodes that take part, so an
		// authorization for one that does not would be a promise nothing
		// keeps — and it would immediately refuse to let the node be forgotten
		// or restarted.
		return refuse(server.CodeServerInactive, "it has been deactivated here, and nothing replicates to it")
	default:
		return nil
	}
}

// grant writes the decision, into the row the pair already has when there is
// one.
func (a *AuthorizeReplica) grant(ctx context.Context, input Input) (*replica.Replica, error) {
	existing, err := a.replicas.GetByPair(ctx, input.UserID, input.ServerID)

	switch {
	case err == nil:
		existing.Grant(input.ReplicatesFiles, a.clock.Now())

		return existing, a.replicas.Update(ctx, existing)
	case errors.Is(err, errs.KindNotFound):
		granted, newErr := replica.New(input.UserID, input.ServerID, input.ReplicatesFiles, a.clock.Now())
		if newErr != nil {
			return nil, newErr
		}

		return granted, a.replicas.Create(ctx, granted)
	default:
		return nil, err
	}
}
