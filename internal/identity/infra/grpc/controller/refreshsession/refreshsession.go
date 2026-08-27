// Package refreshsession serves AuthService.RefreshSession.
package refreshsession

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/refresh"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// RefreshSession serves the call.
type RefreshSession struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(refresh command.Usecase[usecase.Input, usecase.Output]) *RefreshSession {
	return &RefreshSession{usecase: refresh}
}

// Handle exchanges the credential for a new session.
func (c *RefreshSession) Handle(
	ctx context.Context,
	request *quirev1.RefreshSessionRequest,
) (*quirev1.RefreshSessionResponse, error) {
	output, err := c.usecase.Execute(ctx, usecase.Input{RefreshToken: request.GetRefreshToken()})
	if err != nil {
		return nil, err
	}

	return &quirev1.RefreshSessionResponse{Session: convert.Session(&output.Session)}, nil
}
