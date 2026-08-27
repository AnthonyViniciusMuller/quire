// Package convert renders what the identity use cases return into the messages
// of the contract.
//
// It is one package rather than a method on each entity, because the direction
// is the point: the domain must not know that a protobuf exists. Everything
// here reads a domain value and writes a wire value, and nothing here decides
// anything — a controller that needed a decision would be a use case written in
// the wrong place.
package convert

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// OwnUser renders a reader for themselves.
//
// The name says whose reply it belongs in. The address is personal data kept
// out of the replicated set (RN09) and absent from every reply but this one, so
// a second function rendering a reader for anybody else would have to leave it
// out — and having one function with a flag is how the flag ends up wrong.
func OwnUser(record *user.User, federatedID user.FederatedID) *quirev1.User {
	rendered := &quirev1.User{
		Id:                 record.ID.String(),
		LocalName:          record.LocalName.String(),
		DisplayName:        record.DisplayName.String(),
		OriginServerDomain: federatedID.Domain.String(),
		FederatedId:        federatedID.String(),
		CreatedAt:          timestamppb.New(record.CreatedAt),
	}

	if !record.Email.IsZero() {
		address := record.Email.String()
		rendered.Email = &address
	}

	return rendered
}

// Device renders one appliance.
func Device(appliance *device.Device) *quirev1.Device {
	rendered := &quirev1.Device{
		Id:       appliance.ID.String(),
		Name:     appliance.Name.String(),
		Platform: appliance.Platform.String(),
		Active:   appliance.Active,
	}

	// Absent until the first completed synchronization, which is a different
	// thing from the zero instant and has to render as absent.
	if appliance.HasSynced() {
		rendered.LastSyncedAt = timestamppb.New(appliance.LastSyncedAt)
	}

	return rendered
}

// Devices renders a reader's appliances, keeping the order they came in.
func Devices(appliances []*device.Device) []*quirev1.Device {
	rendered := make([]*quirev1.Device, 0, len(appliances))
	for _, appliance := range appliances {
		rendered = append(rendered, Device(appliance))
	}

	return rendered
}

// Session renders the pair of credentials a device is issued.
func Session(session *service.Session) *quirev1.Session {
	return &quirev1.Session{
		AccessToken:           session.AccessToken,
		AccessTokenExpiresAt:  timestamppb.New(session.AccessTokenExpiresAt),
		RefreshToken:          session.RefreshToken,
		RefreshTokenExpiresAt: timestamppb.New(session.RefreshTokenExpiresAt),
	}
}
