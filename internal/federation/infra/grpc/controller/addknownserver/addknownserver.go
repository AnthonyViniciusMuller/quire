// Package addknownserver serves FederationService.AddKnownServer (UC12,
// create).
package addknownserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/addserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// AddKnownServer serves the call.
type AddKnownServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(add command.Usecase[usecase.Input, usecase.Output]) *AddKnownServer {
	return &AddKnownServer{usecase: add}
}

// Handle discovers the domain and records what it found.
//
// The request carries a domain and nothing else, which is the contract's
// decision and not this controller's: a reader who could type the base URL or
// the pin by hand could also type the wrong one, and the pin is the anchor
// every later node-to-node call is checked against.
func (c *AddKnownServer) Handle(
	ctx context.Context,
	request *quirev1.AddKnownServerRequest,
) (*quirev1.AddKnownServerResponse, error) {
	output, err := c.usecase.Execute(ctx, usecase.Input{Domain: request.GetDomain()})
	if err != nil {
		return nil, err
	}

	return &quirev1.AddKnownServerResponse{Server: convert.Server(output.Server)}, nil
}
