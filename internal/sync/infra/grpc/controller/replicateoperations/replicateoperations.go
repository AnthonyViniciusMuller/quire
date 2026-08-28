// Package replicateoperations serves SyncService.ReplicateOperations, the one
// call in this contract whose caller is a peer node and not a reader's device.
package replicateoperations

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/replicateoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/peerauthn"
)

// ReplicateOperations serves the call.
type ReplicateOperations struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(replicate command.Usecase[usecase.Input, usecase.Output]) *ReplicateOperations {
	return &ReplicateOperations{usecase: replicate}
}

// Handle stores and reconciles what the peer offered.
//
// The caller is read off the connection rather than out of the request, exactly
// as a device's identity is read off its token: the pin is what the transport
// established, and a field a caller filled in would be a claim rather than a
// credential. What it is checked against is the use case's business, because
// refusing a peer is a decision.
func (r *ReplicateOperations) Handle(
	ctx context.Context, request *quirev1.ReplicateOperationsRequest,
) (*quirev1.ReplicateOperationsResponse, error) {
	pin, err := peerauthn.Require(ctx)
	if err != nil {
		return nil, err
	}

	reader, err := identifier.User(request.GetUserId())
	if err != nil {
		return nil, err
	}

	changes, err := convert.Changes(request.GetOperations())
	if err != nil {
		return nil, err
	}

	output, err := r.usecase.Execute(ctx, usecase.Input{
		Pin:        pin,
		UserID:     reader,
		Operations: changes,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ReplicateOperationsResponse{
		Results: convert.Results(output.Results),
	}, nil
}
