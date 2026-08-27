// Package removeknownserver serves FederationService.RemoveKnownServer (UC12,
// delete).
package removeknownserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/removeserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// RemoveKnownServer serves the call.
type RemoveKnownServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(remove command.Usecase[usecase.Input, usecase.Output]) *RemoveKnownServer {
	return &RemoveKnownServer{usecase: remove}
}

// Handle forgets the node.
func (c *RemoveKnownServer) Handle(
	ctx context.Context,
	request *quirev1.RemoveKnownServerRequest,
) (*quirev1.RemoveKnownServerResponse, error) {
	serverID, err := identifier.Server(request.GetServerId())
	if err != nil {
		return nil, err
	}

	if _, err := c.usecase.Execute(ctx, usecase.Input{ServerID: serverID}); err != nil {
		return nil, err
	}

	return &quirev1.RemoveKnownServerResponse{}, nil
}
