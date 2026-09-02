// Package admitreplica serves FederationService.AdmitReplica, one of the two
// calls of this service whose caller is a peer node and not a reader's device.
package admitreplica

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/admitreplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/peerauthn"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// AdmitReplica serves the call.
type AdmitReplica struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(admit command.Usecase[usecase.Input, usecase.Output]) *AdmitReplica {
	return &AdmitReplica{usecase: admit}
}

// Handle records what the origin told this node.
//
// The caller is read off the connection rather than out of the request,
// exactly as a device's identity is read off its token: the pin is what the
// transport established, and a field a caller filled in would be a claim
// rather than a credential. What it is checked against is the use case's
// business, because refusing a peer is a decision.
func (c *AdmitReplica) Handle(
	ctx context.Context, request *quirev1.AdmitReplicaRequest,
) (*quirev1.AdmitReplicaResponse, error) {
	pin, err := peerauthn.Require(ctx)
	if err != nil {
		return nil, err
	}

	readerID, err := identifier.User(request.GetReader().GetId())
	if err != nil {
		return nil, err
	}

	devices := make([]service.Device, 0, len(request.GetDevices()))

	for _, appliance := range request.GetDevices() {
		deviceID, parseErr := identifier.Device(appliance.GetId())
		if parseErr != nil {
			return nil, parseErr
		}

		devices = append(devices, service.Device{
			ID:       deviceID,
			Name:     appliance.GetName(),
			Platform: appliance.GetPlatform(),
		})
	}

	_, err = c.usecase.Execute(ctx, usecase.Input{
		Pin: pin,
		Reader: service.Reader{
			ID:          readerID,
			LocalName:   request.GetReader().GetLocalName(),
			DisplayName: request.GetReader().GetDisplayName(),
		},
		Devices:         devices,
		ReplicatesFiles: request.GetReplicatesFiles(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.AdmitReplicaResponse{}, nil
}
