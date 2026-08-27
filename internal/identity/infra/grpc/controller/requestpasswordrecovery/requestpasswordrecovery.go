// Package requestpasswordrecovery serves AuthService.RequestPasswordRecovery
// (UC08, first half).
package requestpasswordrecovery

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/requestrecovery"
)

// RequestPasswordRecovery serves the call.
type RequestPasswordRecovery struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(request command.Usecase[usecase.Input, usecase.Output]) *RequestPasswordRecovery {
	return &RequestPasswordRecovery{usecase: request}
}

// Handle sends the recovery, when there is somebody to send it to. The reply is
// empty either way, which is what stops the call from saying who is registered
// here.
func (c *RequestPasswordRecovery) Handle(
	ctx context.Context,
	request *quirev1.RequestPasswordRecoveryRequest,
) (*quirev1.RequestPasswordRecoveryResponse, error) {
	_, err := c.usecase.Execute(ctx, usecase.Input{Email: request.GetEmail()})
	if err != nil {
		return nil, err
	}

	return &quirev1.RequestPasswordRecoveryResponse{}, nil
}
