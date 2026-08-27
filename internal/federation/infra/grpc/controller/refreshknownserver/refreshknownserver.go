// Package refreshknownserver serves FederationService.RefreshKnownServer
// (UC12, update; RF14).
package refreshknownserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/refreshserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// RefreshKnownServer serves the call.
type RefreshKnownServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(refresh command.Usecase[usecase.Input, usecase.Output]) *RefreshKnownServer {
	return &RefreshKnownServer{usecase: refresh}
}

// Handle re-runs discovery against the node and reports what changed.
//
// The reply says whether the node now presents a different public key. It is
// the one field of this contract that exists to be looked at by a person: a
// deliberate rotation and an interception are indistinguishable from here, and
// the reader is the only party that can tell them apart (C12).
func (c *RefreshKnownServer) Handle(
	ctx context.Context,
	request *quirev1.RefreshKnownServerRequest,
) (*quirev1.RefreshKnownServerResponse, error) {
	serverID, err := identifier.Server(request.GetServerId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{ServerID: serverID})
	if err != nil {
		return nil, err
	}

	return &quirev1.RefreshKnownServerResponse{
		Server:                        convert.Server(output.Server),
		CertificateFingerprintChanged: output.FingerprintChanged,
	}, nil
}
