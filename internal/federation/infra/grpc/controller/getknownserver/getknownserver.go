// Package getknownserver serves FederationService.GetKnownServer (UC12, read).
package getknownserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/getserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// GetKnownServer serves the call.
type GetKnownServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(get command.Usecase[usecase.Input, usecase.Output]) *GetKnownServer {
	return &GetKnownServer{usecase: get}
}

// Handle answers with the node.
func (c *GetKnownServer) Handle(
	ctx context.Context,
	request *quirev1.GetKnownServerRequest,
) (*quirev1.GetKnownServerResponse, error) {
	serverID, err := identifier.Server(request.GetServerId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{ServerID: serverID})
	if err != nil {
		return nil, err
	}

	return &quirev1.GetKnownServerResponse{Server: convert.Server(output.Server)}, nil
}
