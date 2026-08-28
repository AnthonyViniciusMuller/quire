// Package pulloperations serves SyncService.PullOperations (UC09 inbound to the
// device, RN06).
package pulloperations

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pulloperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/convert"
)

// PullOperations serves the call.
type PullOperations struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(pull command.Usecase[usecase.Input, usecase.Output]) *PullOperations {
	return &PullOperations{usecase: pull}
}

// Handle answers with one page of the reader's log.
//
// The reader comes from the token and never from the request. The device does
// not come into it at all: the log is the reader's and not the appliance's, and
// which of their devices is asking changes nothing about what is owed.
func (p *PullOperations) Handle(
	ctx context.Context, request *quirev1.PullOperationsRequest,
) (*quirev1.PullOperationsResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	output, err := p.usecase.Execute(ctx, usecase.Input{
		UserID:        identity.UserID,
		AfterPosition: request.GetAfterPosition(),
		Limit:         int(request.GetLimit()),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.PullOperationsResponse{
		Operations:   convert.Operations(output.Operations),
		LastPosition: output.LastPosition,
		HasMore:      output.HasMore,
	}, nil
}
