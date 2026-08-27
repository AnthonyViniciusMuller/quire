// Package changepassword serves AuthService.ChangePassword (UC06, the
// credentials half).
package changepassword

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/changepassword"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
)

// ChangePassword serves the call.
type ChangePassword struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(change command.Usecase[usecase.Input, usecase.Output]) *ChangePassword {
	return &ChangePassword{usecase: change}
}

// Handle replaces the password.
func (c *ChangePassword) Handle(
	ctx context.Context,
	request *quirev1.ChangePasswordRequest,
) (*quirev1.ChangePasswordResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	_, err = c.usecase.Execute(ctx, usecase.Input{
		UserID:          identity.UserID,
		CurrentPassword: request.GetCurrentPassword(),
		NewPassword:     request.GetNewPassword(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ChangePasswordResponse{}, nil
}
