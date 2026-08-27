// Package listreplicaauthorizations serves
// FederationService.ListReplicaAuthorizations (UC15, read).
package listreplicaauthorizations

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listauthorizations"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
)

// ListReplicaAuthorizations serves the call.
type ListReplicaAuthorizations struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListReplicaAuthorizations {
	return &ListReplicaAuthorizations{usecase: list}
}

// Handle answers with which nodes hold a copy of the reader's data, which of
// them hold the files, and which used to.
func (c *ListReplicaAuthorizations) Handle(
	ctx context.Context,
	request *quirev1.ListReplicaAuthorizationsRequest,
) (*quirev1.ListReplicaAuthorizationsResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:          identity.UserID,
		IncludeInactive: request.GetIncludeInactive(),
	})
	if err != nil {
		return nil, err
	}

	rendered := make([]*quirev1.ReplicaAuthorization, 0, len(output.Authorizations))
	for _, authorization := range output.Authorizations {
		rendered = append(rendered,
			convert.Authorization(authorization.Replica, authorization.ServerDomain))
	}

	return &quirev1.ListReplicaAuthorizationsResponse{Authorizations: rendered}, nil
}
