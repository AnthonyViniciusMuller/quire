// Package changeemail serves AuthService.ChangeEmail (UC06, the address).
package changeemail

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/changeemail"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// ChangeEmail serves the call.
type ChangeEmail struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(change command.Usecase[usecase.Input, usecase.Output]) *ChangeEmail {
	return &ChangeEmail{usecase: change}
}

// Handle replaces the address.
func (c *ChangeEmail) Handle(
	ctx context.Context,
	request *quirev1.ChangeEmailRequest,
) (*quirev1.ChangeEmailResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		Password: request.GetPassword(),
		Email:    request.GetEmail(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ChangeEmailResponse{User: convert.OwnUser(output.User, output.FederatedID)}, nil
}
