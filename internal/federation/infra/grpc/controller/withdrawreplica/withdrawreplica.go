// Package withdrawreplica serves FederationService.WithdrawReplica, the
// peer-facing mirror of AdmitReplica.
package withdrawreplica

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/withdrawreplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/peerauthn"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// WithdrawReplica serves the call.
type WithdrawReplica struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(withdraw command.Usecase[usecase.Input, usecase.Output]) *WithdrawReplica {
	return &WithdrawReplica{usecase: withdraw}
}

// Handle deactivates the permission the calling node once carried here.
func (c *WithdrawReplica) Handle(
	ctx context.Context, request *quirev1.WithdrawReplicaRequest,
) (*quirev1.WithdrawReplicaResponse, error) {
	pin, err := peerauthn.Require(ctx)
	if err != nil {
		return nil, err
	}

	readerID, err := identifier.User(request.GetUserId())
	if err != nil {
		return nil, err
	}

	if _, err = c.usecase.Execute(ctx, usecase.Input{Pin: pin, UserID: readerID}); err != nil {
		return nil, err
	}

	return &quirev1.WithdrawReplicaResponse{}, nil
}
