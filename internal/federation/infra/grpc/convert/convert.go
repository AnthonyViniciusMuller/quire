// Package convert renders what the federation use cases return into the
// messages of the contract.
//
// It is one package rather than a method on each entity, because the direction
// is the point: the domain must not know that a protobuf exists. Everything
// here reads a domain value and writes a wire value, and nothing here decides
// anything — a controller that needed a decision would be a use case written
// in the wrong place.
package convert

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// Descriptor renders what a node publishes about itself.
//
// The three optional fields are rendered as absent rather than as empty
// strings. A node that publishes no pin and one that publishes an empty pin
// are different things to a peer deciding whether it may replicate, and the
// wire has a way to say so.
func Descriptor(descriptor *server.Descriptor) *quirev1.ServerDescriptor {
	rendered := &quirev1.ServerDescriptor{
		Domain:  descriptor.Domain.String(),
		BaseUrl: descriptor.BaseURL.String(),
	}

	if !descriptor.JWKSURI.IsZero() {
		uri := descriptor.JWKSURI.String()
		rendered.JwksUri = &uri
	}

	if !descriptor.CertificateFingerprint.IsZero() {
		pin := descriptor.CertificateFingerprint.String()
		rendered.CertificateFingerprint = &pin
	}

	if !descriptor.GRPCAuthority.IsZero() {
		authority := descriptor.GRPCAuthority.String()
		rendered.Grpc = &authority
	}

	return rendered
}

// Server renders one node in the catalogue.
func Server(node *server.Server) *quirev1.Server {
	rendered := &quirev1.Server{
		Id: node.ID.String(),
		// Descriptor_, with the underscore protoc-gen-go adds: Descriptor is
		// the method every generated message already has.
		Descriptor_: Descriptor(&node.Descriptor),
		IsLocal:     node.IsLocal,
		Active:      node.Active,
	}

	// Absent on a row nothing has discovered, which is a different thing from
	// the zero instant and has to render as absent.
	if !node.DiscoveredAt.IsZero() {
		rendered.DiscoveredAt = timestamppb.New(node.DiscoveredAt)
	}

	return rendered
}

// Servers renders the catalogue, keeping the order it came in.
func Servers(nodes []*server.Server) []*quirev1.Server {
	rendered := make([]*quirev1.Server, 0, len(nodes))
	for _, node := range nodes {
		rendered = append(rendered, Server(node))
	}

	return rendered
}

// Authorization renders one permission, together with the domain of the node
// it names.
//
// The domain is a parameter rather than something looked up here, because
// looking it up would be a decision and a controller does not make one. The
// use case that answers with an authorization is the one that read the
// catalogue.
func Authorization(authorization *replica.Replica, domain server.Domain) *quirev1.ReplicaAuthorization {
	return &quirev1.ReplicaAuthorization{
		Id:              authorization.ID.String(),
		ServerId:        authorization.ServerID.String(),
		ServerDomain:    domain.String(),
		AuthorizedAt:    timestamppb.New(authorization.AuthorizedAt),
		ReplicatesFiles: authorization.ReplicatesFiles,
		Active:          authorization.Active,
	}
}
