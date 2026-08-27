// Package resetpassword serves AuthService.ResetPassword (UC08, second half).
package resetpassword

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/resetpassword"
)

// ResetPassword serves the call.
type ResetPassword struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(reset command.Usecase[usecase.Input, usecase.Output]) *ResetPassword {
	return &ResetPassword{usecase: reset}
}

// Handle consumes the credential and sets the password.
func (c *ResetPassword) Handle(
	ctx context.Context,
	request *quirev1.ResetPasswordRequest,
) (*quirev1.ResetPasswordResponse, error) {
	_, err := c.usecase.Execute(ctx, usecase.Input{
		RecoveryToken: request.GetRecoveryToken(),
		NewPassword:   request.GetNewPassword(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ResetPasswordResponse{}, nil
}
