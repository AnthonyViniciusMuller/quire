// Package discoverserver serves FederationService.DiscoverServer (UC13, RF14).
package discoverserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/discover"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// DiscoverServer serves the call.
type DiscoverServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(discover command.Usecase[usecase.Input, usecase.Output]) *DiscoverServer {
	return &DiscoverServer{usecase: discover}
}

// Handle answers with what the domain publishes about itself.
//
// It reads no identity, and it is still an authenticated method: the
// interceptor denies by default and this call is not on the public list.
// That is deliberate. The call makes this node fetch a URL the caller named,
// and a stranger who could do that would have an outbound request they aim —
// while a reader's own application can make the same RFC 8615 lookup itself,
// which is what it does before it has an account anywhere.
func (c *DiscoverServer) Handle(
	ctx context.Context,
	request *quirev1.DiscoverServerRequest,
) (*quirev1.DiscoverServerResponse, error) {
	output, err := c.usecase.Execute(ctx, usecase.Input{Domain: request.GetDomain()})
	if err != nil {
		return nil, err
	}

	return &quirev1.DiscoverServerResponse{Descriptor_: convert.Descriptor(output.Descriptor)}, nil
}
