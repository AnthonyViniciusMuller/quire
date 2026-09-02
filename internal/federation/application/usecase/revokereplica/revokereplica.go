// Package revokereplica is the withdrawing half of UC15 (RF16).
//
// The authorization is deactivated and kept. Revoking stops the replication;
// it does not reach into another operator's database, and the record that the
// permission once existed is what explains a peer that still holds data — a
// reader who cannot see that record cannot act on it.
//
// It is also the one call in UC12 and UC15 that is a reader's alone. Stopping
// a node in the catalogue is node-wide and refused while anybody authorizes
// it (C15); this withdraws one reader's own permission and touches nothing
// anybody else decided.
package revokereplica

import (
	"context"
	"log/slog"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// RevokeReplica withdraws a permission a reader granted.
type RevokeReplica struct {
	replicas replica.Repository
	peers    service.Peers
}

// RevokeReplica satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RevokeReplica)(nil)

// New returns the use case over its dependencies.
func New(replicas replica.Repository, peers service.Peers) *RevokeReplica {
	return &RevokeReplica{replicas: replicas, peers: peers}
}

// Execute deactivates the permission, and tells the node.
//
// The revocation is this node's and is what protects the reader: nothing is
// offered to a node the permission no longer names. Telling the node is the
// mirror of C22, so that a revoked replica stops accepting rather than
// merely stops being sent things, and it is attempted once and not insisted
// on — a node that could not be told holds a permission that nothing here
// will ever act on, and a revocation that failed because another operator's
// node was down would be a reader unable to withdraw what is theirs.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (r *RevokeReplica) Execute(ctx context.Context, input Input) (Output, error) {
	authorization, err := r.replicas.GetByPair(ctx, input.UserID, input.ServerID)
	if err != nil {
		return Output{}, err
	}

	authorization.Revoke()

	if err = r.replicas.Update(ctx, authorization); err != nil {
		return Output{}, err
	}

	if err = r.peers.Withdraw(ctx, input.ServerID, input.UserID); err != nil {
		logging.From(ctx).WarnContext(ctx, "a node could not be told the reader withdrew its permission",
			slog.String("server_id", input.ServerID.String()),
			slog.String("user_id", input.UserID.String()), logging.Err(err))
	}

	return Output{}, nil
}
