// Package withdrawreplica is the mirror of admitreplica: a peer telling this
// node that a reader has withdrawn the permission, so that a revoked replica
// stops accepting rather than merely stops being sent things (C22).
package withdrawreplica

import (
	"context"
	"errors"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/admitreplica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// WithdrawReplica deactivates the permission a peer once carried here.
type WithdrawReplica struct {
	servers  server.Repository
	replicas replica.Repository
}

// WithdrawReplica satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*WithdrawReplica)(nil)

// New returns the use case over its dependencies.
func New(servers server.Repository, replicas replica.Repository) *WithdrawReplica {
	return &WithdrawReplica{servers: servers, replicas: replicas}
}

// Execute deactivates the permission, and keeps the row as the origin keeps
// its own: the record that the permission once existed is what explains why
// this node still holds the reader's data.
//
// A permission this node never recorded is nothing to withdraw, and the call
// succeeds: the origin retries what did not answer, and an answer that made a
// second withdrawal fail would make the first one look lost.
func (w *WithdrawReplica) Execute(ctx context.Context, input Input) (Output, error) {
	origin, err := admitreplica.Caller(ctx, w.servers, input.Pin)
	if err != nil {
		return Output{}, err
	}

	authorization, err := w.replicas.GetByPair(ctx, input.UserID, origin.ID)
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			return Output{}, nil
		}

		return Output{}, err
	}

	authorization.Revoke()

	return Output{}, w.replicas.Update(ctx, authorization)
}
