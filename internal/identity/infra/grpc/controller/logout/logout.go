// Package logout serves AuthService.Logout (UC07, second half).
package logout

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/logout"
)

// Logout serves the call.
type Logout struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(logout command.Usecase[usecase.Input, usecase.Output]) *Logout {
	return &Logout{usecase: logout}
}

// Handle ends the session the credential belongs to.
func (c *Logout) Handle(ctx context.Context, request *quirev1.LogoutRequest) (*quirev1.LogoutResponse, error) {
	_, err := c.usecase.Execute(ctx, usecase.Input{RefreshToken: request.GetRefreshToken()})
	if err != nil {
		return nil, err
	}

	return &quirev1.LogoutResponse{}, nil
}
