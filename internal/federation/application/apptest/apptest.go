// Package apptest holds the doubles the federation use case tests are written
// against.
//
// It is a package rather than a fixture repeated in every test file because
// the use cases of this slice depend on the same handful of ports, and a
// double written eight times drifts eight ways. It is imported only by tests.
//
// The doubles are fakes and not mocks: they behave, rather than record. The
// discovery double in particular answers out of a catalogue of documents, so
// that a test can exercise a peer that publishes no pin — a real case, and one
// no assertion about a call count would describe.
package apptest

import (
	"context"
	"sync"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// Discovery answers lookups out of the documents a test gave it.
type Discovery struct {
	mu        sync.Mutex
	published map[server.Domain]*server.Descriptor
	lookups   []server.Domain

	// Err, when set, is what Discover reports without answering — for the test
	// that needs a peer which is down.
	Err error
}

// Discovery satisfies the port the use cases hold.
var _ service.Discovery = (*Discovery)(nil)

// NewDiscovery returns a client that knows about nothing.
func NewDiscovery() *Discovery {
	return &Discovery{published: map[server.Domain]*server.Descriptor{}}
}

// Publish makes domain answer with descriptor, as a node that serves a
// document does.
func (d *Discovery) Publish(descriptor *server.Descriptor) {
	d.mu.Lock()
	defer d.mu.Unlock()

	stored := *descriptor
	d.published[descriptor.Domain] = &stored
}

// Discover answers with what the domain publishes.
func (d *Discovery) Discover(_ context.Context, domain server.Domain) (*server.Descriptor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lookups = append(d.lookups, domain)

	if d.Err != nil {
		return nil, d.Err
	}

	descriptor, found := d.published[domain]
	if !found {
		return nil, errs.New(errs.KindFailedPrecondition,
			"that domain does not publish a quire discovery document").
			WithCode(service.CodeNotAQuireServer)
	}

	// Copied out, so that a caller mutating what it read does not reach back
	// into the document the node publishes.
	answer := *descriptor

	return &answer, nil
}

// Lookups is every domain the client was asked about, in order. It is how a
// test asserts that a use case which should have stored something instead
// asked nobody, and the other way round.
func (d *Discovery) Lookups() []server.Domain {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]server.Domain(nil), d.lookups...)
}

// Descriptor is a node as a well-formed document describes it, which most
// tests need one of and none of them need to spell out.
func Descriptor(domain server.Domain) *server.Descriptor {
	return &server.Descriptor{
		Domain:                 domain,
		BaseURL:                server.BaseURL("https://" + domain.String()),
		JWKSURI:                server.JWKSURI("https://" + domain.String() + "/.well-known/jwks.json"),
		CertificateFingerprint: server.Fingerprint(wellknown.PinPrefix + "Zm9vYmFyCg=="),
		GRPCAuthority:          server.GRPCAuthority(domain.String() + ":9090"),
	}
}
