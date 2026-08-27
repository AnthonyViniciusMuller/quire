// Package listknownservers serves FederationService.ListKnownServers (UC12,
// read).
package listknownservers

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listservers"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// ListKnownServers serves the call.
type ListKnownServers struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListKnownServers {
	return &ListKnownServers{usecase: list}
}

// Handle answers with the catalogue.
func (c *ListKnownServers) Handle(
	ctx context.Context,
	request *quirev1.ListKnownServersRequest,
) (*quirev1.ListKnownServersResponse, error) {
	output, err := c.usecase.Execute(ctx,
		usecase.Input{IncludeInactive: request.GetIncludeInactive()})
	if err != nil {
		return nil, err
	}

	return &quirev1.ListKnownServersResponse{Servers: convert.Servers(output.Servers)}, nil
}
