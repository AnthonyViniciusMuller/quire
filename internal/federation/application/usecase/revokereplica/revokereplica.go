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

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
)

// RevokeReplica withdraws permissions.
type RevokeReplica struct {
	replicas replica.Repository
}

// RevokeReplica satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*RevokeReplica)(nil)

// New returns the use case over its dependencies.
func New(replicas replica.Repository) *RevokeReplica {
	return &RevokeReplica{replicas: replicas}
}

// Execute withdraws the permission.
//
// Revoking one that is already revoked succeeds and writes the same row again.
// The reader asked for a state and it is the state they get, and a call that
// failed the second time would have a client showing an error for a node it
// has already stopped.
//
// It takes no lock and opens no unit of work: one row is read and written, and
// the calls a lock would serialize it against are the ones that grant, not the
// ones that withdraw.
func (r *RevokeReplica) Execute(ctx context.Context, input Input) (Output, error) {
	authorization, err := r.replicas.GetByPair(ctx, input.UserID, input.ServerID)
	if err != nil {
		return Output{}, err
	}

	authorization.Revoke()

	return Output{}, r.replicas.Update(ctx, authorization)
}
