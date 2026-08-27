// Package revokereplica serves FederationService.RevokeReplica (UC15, RF16).
package revokereplica

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/revokereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
)

// RevokeReplica serves the call.
type RevokeReplica struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(revoke command.Usecase[usecase.Input, usecase.Output]) *RevokeReplica {
	return &RevokeReplica{usecase: revoke}
}

// Handle withdraws the permission.
func (c *RevokeReplica) Handle(
	ctx context.Context,
	request *quirev1.RevokeReplicaRequest,
) (*quirev1.RevokeReplicaResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	serverID, err := identifier.Server(request.GetServerId())
	if err != nil {
		return nil, err
	}

	if _, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		ServerID: serverID,
	}); err != nil {
		return nil, err
	}

	return &quirev1.RevokeReplicaResponse{}, nil
}
