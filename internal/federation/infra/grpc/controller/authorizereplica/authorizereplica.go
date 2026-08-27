// Package authorizereplica serves FederationService.AuthorizeReplica (UC15,
// RF16).
package authorizereplica

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/authorizereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
)

// AuthorizeReplica serves the call.
type AuthorizeReplica struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(authorize command.Usecase[usecase.Input, usecase.Output]) *AuthorizeReplica {
	return &AuthorizeReplica{usecase: authorize}
}

// Handle grants the permission.
//
// The reader comes from the token and never from the request. RN03 is a
// promise that data leaves this node only with the reader's permission, and a
// permission somebody else could grant on their behalf would not be one.
func (c *AuthorizeReplica) Handle(
	ctx context.Context,
	request *quirev1.AuthorizeReplicaRequest,
) (*quirev1.AuthorizeReplicaResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	serverID, err := identifier.Server(request.GetServerId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:          identity.UserID,
		ServerID:        serverID,
		ReplicatesFiles: request.GetReplicatesFiles(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.AuthorizeReplicaResponse{
		Authorization: convert.Authorization(output.Authorization, output.ServerDomain),
	}, nil
}
