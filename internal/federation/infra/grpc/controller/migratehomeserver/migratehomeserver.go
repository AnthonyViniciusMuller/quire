// Package migratehomeserver serves FederationService.MigrateHomeServer (UC16,
// RF17).
//
// The call is on the federation service and the work is the identity slice's,
// and both of those are right. What UC16 changes is which node a reader belongs
// to, which is a fact about the federation; what it writes is an account, its
// devices and a session, which is the only slice that holds any of them. So the
// controller is here, beside the other nine calls of this service, and the use
// case is there, beside registering and logging in — which is what it is.
//
// It is the one place where a controller of one slice translates for a use case
// of another. The alternative is a port in this slice that an adapter satisfies
// out of the identity slice's repositories, and what that would buy is a second
// vocabulary for a reader and a device, in the slice that has no business
// defining either.
package migratehomeserver

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/identity/application/usecase/migratehomeserver"
	identityconvert "github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/convert"
)

// MigrateHomeServer serves the call.
type MigrateHomeServer struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(migrate command.Usecase[usecase.Input, usecase.Output]) *MigrateHomeServer {
	return &MigrateHomeServer{usecase: migrate}
}

// Handle takes the reader in.
//
// Nothing is read from a session, because there is none: a reader migrating to
// this node has no account here yet, which is why the method is public. The
// previous identifier is carried through as the caller sent it and is recorded
// rather than believed — this node cannot check it, and C11 in
// docs/tcc-corrections.md says so in the contract itself.
func (m *MigrateHomeServer) Handle(
	ctx context.Context, request *quirev1.MigrateHomeServerRequest,
) (*quirev1.MigrateHomeServerResponse, error) {
	arriving := request.GetDevices()

	devices := make([]usecase.Arrival, 0, len(arriving))
	for _, appliance := range arriving {
		devices = append(devices, usecase.Arrival{
			ID:       appliance.GetId(),
			Name:     appliance.GetName(),
			Platform: appliance.GetPlatform(),
		})
	}

	output, err := m.usecase.Execute(ctx, usecase.Input{
		LocalName:           request.GetLocalName(),
		DisplayName:         request.GetDisplayName(),
		Email:               request.GetEmail(),
		Password:            request.GetPassword(),
		PreviousFederatedID: request.GetPreviousFederatedId(),
		Devices:             devices,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.MigrateHomeServerResponse{
		User:    identityconvert.OwnUser(output.User, output.FederatedID),
		Devices: identityconvert.Devices(output.Devices),
		Session: identityconvert.Session(&output.Session),
	}, nil
}
