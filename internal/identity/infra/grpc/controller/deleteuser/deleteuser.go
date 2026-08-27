// Package deleteuser serves AuthService.DeleteUser (UC06, delete).
package deleteuser

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/deleteuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
)

// DeleteUser serves the call.
type DeleteUser struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(remove command.Usecase[usecase.Input, usecase.Output]) *DeleteUser {
	return &DeleteUser{usecase: remove}
}

// Handle removes the reader from this node.
func (c *DeleteUser) Handle(
	ctx context.Context,
	request *quirev1.DeleteUserRequest,
) (*quirev1.DeleteUserResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	_, err = c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, Password: request.GetPassword()})
	if err != nil {
		return nil, err
	}

	return &quirev1.DeleteUserResponse{}, nil
}
