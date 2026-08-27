// Package registeruser serves AuthService.RegisterUser (UC14).
package registeruser

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// RegisterUser serves the call.
type RegisterUser struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(register command.Usecase[usecase.Input, usecase.Output]) *RegisterUser {
	return &RegisterUser{usecase: register}
}

// Handle registers the reader.
func (c *RegisterUser) Handle(
	ctx context.Context,
	request *quirev1.RegisterUserRequest,
) (*quirev1.RegisterUserResponse, error) {
	output, err := c.usecase.Execute(ctx, usecase.Input{
		LocalName:   request.GetLocalName(),
		DisplayName: request.GetDisplayName(),
		Email:       request.GetEmail(),
		Password:    request.GetPassword(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.RegisterUserResponse{User: convert.OwnUser(output.User, output.FederatedID)}, nil
}
