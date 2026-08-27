// Package getuser serves AuthService.GetUser (UC06, read).
package getuser

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/getuser"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// GetUser serves the call.
type GetUser struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(get command.Usecase[usecase.Input, usecase.Output]) *GetUser {
	return &GetUser{usecase: get}
}

// Handle answers the caller with their own record.
//
// The reader is taken from the session and the request carries no identifier at
// all, which is the access rule itself: a node never serves one reader's record
// to another.
func (c *GetUser) Handle(ctx context.Context, _ *quirev1.GetUserRequest) (*quirev1.GetUserResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID})
	if err != nil {
		return nil, err
	}

	return &quirev1.GetUserResponse{User: convert.OwnUser(output.User, output.FederatedID)}, nil
}
