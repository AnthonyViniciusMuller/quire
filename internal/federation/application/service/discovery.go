package service

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// The stable machine-readable codes a discovery adapter reports. They are
// declared with the port and not with the adapter, because they are what a use
// case and, through it, a client branches on — and a second adapter would have
// to raise the same ones.
const (
	// CodeDiscoveryUnreachable is a domain that did not answer: no route, no
	// listener, a TLS handshake that failed, or a lookup that ran past the
	// timeout. It is the one code here that is worth retrying.
	CodeDiscoveryUnreachable = "discovery_unreachable"
	// CodeNotAQuireServer is a domain that answered with something that is not
	// a Quire discovery document: a 404, a login page, valid JSON describing
	// something else. The host exists and is not a node.
	CodeNotAQuireServer = "not_a_quire_server"
	// CodeInsecureDiscovery is a lookup this node refused to make, or to
	// follow, over plain HTTP. The pin a document carries is trustworthy only
	// as far as the channel it arrived on.
	CodeInsecureDiscovery = "insecure_discovery"
)

// Discovery resolves a domain to what the node at it publishes about itself,
// over /.well-known as RFC 8615 establishes (UC13, RF14).
//
// It is the mechanism the whole federation rests on. A reader types a domain
// and their application knows nothing else; discovery is what turns it into an
// origin, a signing key location, an address to dial and a pin to check that
// node against, and every federated exchange assumes it has happened.
//
// It stores nothing. What the answer is worth keeping is a decision for the
// use case that asked, which is what tells UC13 — a lookup — from the first
// half of adding a node to the catalogue.
type Discovery interface {
	// Discover fetches the peer document of domain and returns it as a
	// descriptor.
	//
	// The domain of the descriptor is the one asked for and never one the
	// document claims: the document has no such field precisely so that a node
	// cannot answer for an authority it was not addressed at.
	Discover(ctx context.Context, domain server.Domain) (*server.Descriptor, error)
}
