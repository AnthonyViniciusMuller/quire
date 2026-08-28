// Package pushoperations serves SyncService.PushOperations (UC09 outbound from
// the device, UC11).
package pushoperations

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/convert"
)

// PushOperations serves the call.
type PushOperations struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(push command.Usecase[usecase.Input, usecase.Output]) *PushOperations {
	return &PushOperations{usecase: push}
}

// Handle stores and reconciles the batch.
//
// The reader and the device both come from the token, and the device is handed
// on as the author every change must declare — which is RN10, checked by the
// use case because refusing a batch is a decision. What the request carries is
// the changes and nothing about who is offering them.
func (p *PushOperations) Handle(
	ctx context.Context, request *quirev1.PushOperationsRequest,
) (*quirev1.PushOperationsResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	changes, err := convert.Changes(request.GetOperations())
	if err != nil {
		return nil, err
	}

	output, err := p.usecase.Execute(ctx, usecase.Input{
		UserID:     identity.UserID,
		Author:     identity.DeviceID,
		Operations: changes,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.PushOperationsResponse{
		Results:      convert.Results(output.Results),
		LastPosition: output.LastPosition,
	}, nil
}
