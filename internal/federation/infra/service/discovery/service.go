// Package discovery is the .well-known client: it turns a domain into what the
// node at it publishes about itself (UC13, RF14).
//
// It satisfies the Discovery port of the federation slice's application layer,
// and it reads the documents defined in internal/shared/wellknown — the same
// types this node serves its own from, so that a field the publisher writes
// and the reader ignores cannot exist.
//
// The security of the whole federation rests on this fetch, and on nothing
// else. The pin a peer is checked against for node-to-node mTLS (RNF08) is a
// value read out of that document, so it is exactly as trustworthy as the
// channel it arrived on: an attacker who can answer for the domain over TLS
// this node accepts can publish any pin they like. That is inherent to
// trust-on-first-use and is why re-pinning is a deliberate act by the reader
// (C12) — and it is why plain HTTP is refused outside the development profile,
// and why a redirect may not downgrade to it.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// opDiscover is the operation reported by this file, in the form the errs
// package expects.
const opDiscover = "federation/discovery: discover"

// maxDocumentSize bounds what is read from a peer. The document is four short
// strings; a megabyte is already three orders of magnitude more than one, and
// without a bound a peer could answer a lookup with an endless body and hold
// this node's memory for as long as it kept writing.
const maxDocumentSize = 1 << 20

// maxRedirects bounds how far a lookup is followed. Two is enough for the
// redirect deployments actually use — an apex to a canonical host — and a
// chain longer than that is either a loop or a lookup being walked somewhere
// nobody addressed.
const maxRedirects = 2

// Service fetches discovery documents over HTTP.
type Service struct {
	client *http.Client

	// insecure allows the lookup to be made, and followed, over plain HTTP. It
	// is the development profile: the local federation runs without
	// certificates, and the configuration refuses this outside development.
	insecure bool
}

// Service satisfies the port the use cases hold.
var _ service.Discovery = (*Service)(nil)

// New returns the client configured for this node.
//
// The timeout is on the client rather than only on the context, so that a peer
// that accepts the connection and then writes nothing cannot hold a call open:
// a caller's own deadline bounds it too, and whichever is shorter wins.
func New(cfg *config.Federation) *Service {
	insecure := cfg.AllowInsecureDiscovery

	return &Service{
		insecure: insecure,
		client: &http.Client{
			Timeout: cfg.DiscoveryTimeout,
			// A refusal here is raised already classified, rather than as a
			// bare error: the client wraps whatever this returns in a
			// *url.Error, and an error that arrived with its kind and code
			// intact can be told apart from a peer that was simply
			// unreachable.
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					return refuse("it was redirected more than " + strconv.Itoa(maxRedirects) + " times")
				}

				if request.URL.Scheme != "https" && !insecure {
					return refuse("it was redirected to plain http, which would carry the pin in the clear")
				}

				return nil
			},
		},
	}
}

// Discover fetches the peer document of domain.
func (s *Service) Discover(ctx context.Context, domain server.Domain) (*server.Descriptor, error) {
	if err := domain.Validate(); err != nil {
		return nil, err
	}

	document, err := s.fetch(ctx, s.documentURL(domain))
	if err != nil {
		return nil, err
	}

	// The domain is the one that was asked for. The document carries none, so
	// that a node cannot answer for an authority it was not addressed at, and
	// a descriptor whose domain came from the body would be exactly that.
	descriptor := &server.Descriptor{
		Domain:                 domain,
		BaseURL:                server.BaseURL(document.Server.BaseURL),
		JWKSURI:                server.JWKSURI(document.Server.JWKSURI),
		CertificateFingerprint: server.Fingerprint(document.Server.CertificateFingerprint),
		GRPCAuthority:          server.GRPCAuthority(document.Server.GRPC),
	}

	// A document this node cannot make a record out of is a document from
	// something that is not a Quire node, whatever it called itself. The
	// underlying complaint is kept: it names the field, and only the logs see
	// it.
	if err := descriptor.Validate(); err != nil {
		return nil, errs.Wrap(err, errs.KindFailedPrecondition,
			"that domain does not publish a usable quire discovery document").
			WithOp(opDiscover).
			WithCode(service.CodeNotAQuireServer)
	}

	return descriptor, nil
}

// documentURL is where RFC 8615 says the document is, which is what makes the
// lookup predictable from the domain alone.
func (s *Service) documentURL(domain server.Domain) string {
	scheme := "https"
	if s.insecure {
		scheme = "http"
	}

	return (&url.URL{Scheme: scheme, Host: domain.String(), Path: wellknown.ServerPath}).String()
}

// fetch reads the document at address, or says why it could not.
func (s *Service) fetch(ctx context.Context, address string) (wellknown.ServerDocument, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return wellknown.ServerDocument{}, errs.Wrap(err, errs.KindInvalidArgument,
			"that domain cannot be addressed").
			WithOp(opDiscover).
			WithCode(service.CodeNotAQuireServer)
	}

	request.Header.Set("Accept", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return wellknown.ServerDocument{}, s.transportError(err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		// Every status is one answer here. A 404 is a host that is not a node,
		// and a 500 is a node that cannot say what it is; neither leaves this
		// call with a description, and telling the two apart would only invite
		// a caller to retry the one that will keep failing.
		return wellknown.ServerDocument{}, errs.New(errs.KindFailedPrecondition,
			"that domain does not publish a quire discovery document").
			WithOp(opDiscover).
			WithCode(service.CodeNotAQuireServer).
			WithField("domain", "it answered the lookup with status "+strconv.Itoa(response.StatusCode))
	}

	var document wellknown.ServerDocument

	// Bounded before it is decoded, not after: a decoder handed the raw body
	// would already have read whatever it was sent.
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDocumentSize)).Decode(&document); err != nil {
		return wellknown.ServerDocument{}, errs.Wrap(err, errs.KindFailedPrecondition,
			"that domain answered the lookup with something that is not a quire discovery document").
			WithOp(opDiscover).
			WithCode(service.CodeNotAQuireServer)
	}

	return document, nil
}

// refuse is the error a redirect this node will not follow raises.
func refuse(reason string) error {
	return errs.New(errs.KindFailedPrecondition, "this node will not follow that lookup").
		WithOp(opDiscover).
		WithCode(service.CodeInsecureDiscovery).
		WithField("domain", reason)
}

// transportError says which of the two things went wrong: this node refused to
// make the call, or the peer did not answer it.
func (s *Service) transportError(err error) error {
	// A refusal is not a transport failure. Nothing was unreachable, and
	// reporting it as retryable would have a caller retry a lookup that is
	// refused by policy rather than by circumstance. It arrives classified,
	// inside the *url.Error the client wraps every redirect error in, and the
	// whole chain is kept: the logs still name the address, and the field the
	// refusal named is still what a client is shown, since both codes and
	// fields are read from the outermost error in the chain that carries any.
	var refused *errs.Error
	if errors.As(err, &refused) {
		return errs.Wrap(err, refused.Kind, refused.Message).
			WithOp(opDiscover).
			WithCode(refused.Code)
	}

	return errs.Wrap(err, errs.KindUnavailable, "that domain did not answer the lookup").
		WithOp(opDiscover).
		WithCode(service.CodeDiscoveryUnreachable)
}
