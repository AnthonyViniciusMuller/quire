// Package wellknown publishes what a stranger has to be able to read about
// this node, and defines the documents the discovery client parses back.
//
// RFC 8615 is the whole mechanism of the federation. A reader types
// @anthony:quire-a.example and their application knows nothing but that
// domain; it learns where to call, where the signing keys are, and what
// certificate to trust by fetching a document from a path it can predict.
// UC13 and RF14 are that lookup, and every other federated exchange depends on
// it having happened.
//
// The documents are defined here rather than in the federation slice because
// both ends need the same definition: this node serves them, and the discovery
// client of phase 6 reads them from a peer. One type for both is what keeps a
// field the publisher writes and the reader ignores from ever existing.
//
// The shape follows Matrix, whose .well-known/matrix/client the thesis cites as
// the precedent: a single object keyed by the name of the service, so that a
// document can gain a second service without any reader having to change.
package wellknown

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
)

// The operations reported by this file, in the form the errs package expects.
const opServe = "shared/wellknown: serve"

// The paths the documents are published at, as RFC 8615 requires them to be:
// predictable from the domain alone.
const (
	// ClientPath is what a reader's application reads to find its node.
	ClientPath = "/.well-known/quire/client"
	// ServerPath is what a peer node reads to find this one.
	ServerPath = "/.well-known/quire/server"
	// JWKSPath is where the node publishes the public keys its tokens are
	// signed with (RNF11). The path is named here because the server document
	// points at it; the endpoint itself is served by the identity slice, which
	// is the only part of the node that holds a signing key.
	JWKSPath = "/.well-known/jwks.json"
)

// documentMaxAge is how long the documents may be cached, in seconds. They
// change when an operator changes the deployment, which is rare, and the one
// value with a security meaning — the pinned key — is re-read deliberately
// through RefreshKnownServer rather than by waiting for a cache to expire.
const documentMaxAge = 3600

// ClientDocument is what a reader's application needs in order to reach the
// node hosting its identifier.
type ClientDocument struct {
	// Client is keyed by the name of the service, as Matrix keys its own.
	Client ClientEndpoint `json:"quire.client"`
}

// ClientEndpoint is where an application talks to the node.
type ClientEndpoint struct {
	// BaseURL is the origin serving this document and everything else the node
	// exposes over HTTP.
	BaseURL string `json:"base_url"`
	// GRPC is the authority to dial for the API itself. It is published
	// separately from the base URL because the two are only the same address
	// where a gateway happens to collapse them — see D06 in
	// docs/tcc-corrections.md.
	GRPC string `json:"grpc"`
}

// ServerDocument is what a peer node needs in order to federate with this one.
type ServerDocument struct {
	// Server is keyed by the name of the service.
	Server ServerEndpoint `json:"quire.server"`
}

// ServerEndpoint is everything a peer learns about this node before trusting
// it with anything.
type ServerEndpoint struct {
	// BaseURL is the origin of the node.
	BaseURL string `json:"base_url"`
	// GRPC is the authority node-to-node calls are dialed at (D06).
	GRPC string `json:"grpc"`
	// JWKSURI is where the node publishes the public keys its tokens are
	// signed with, so that a peer verifies them without sharing a secret
	// (RNF11).
	JWKSURI string `json:"jwks_uri,omitempty"`
	// CertificateFingerprint is the pin for node-to-node mTLS (RNF08), in the
	// form spki-sha256:<base64>. It is a digest of the public key and not of
	// the certificate, and C12 in docs/tcc-corrections.md is why: a digest of
	// the certificate stops matching on the first ACME renewal, which would
	// break the replication of every pair of nodes about every sixty days.
	//
	// It is absent where the node has no certificate of its own, which is the
	// development profile.
	CertificateFingerprint string `json:"cert_fingerprint,omitempty"`
}

// NewClientDocument is what this node publishes for the applications of the
// readers it hosts.
func NewClientDocument(cfg *config.Server) ClientDocument {
	return ClientDocument{Client: ClientEndpoint{
		BaseURL: cfg.BaseURL.String(),
		GRPC:    cfg.GRPCAdvertisedAddress,
	}}
}

// NewServerDocument is what this node publishes for its peers.
//
// The fingerprint is computed from the certificate the node presents on
// node-to-node calls. A node configured without one publishes a document
// without a pin, and a peer is then free to refuse it: that is the development
// profile, and refusing it in production is the point of RNF08.
func NewServerDocument(cfg *config.Config) (ServerDocument, error) {
	document := ServerDocument{Server: ServerEndpoint{
		BaseURL: cfg.Server.BaseURL.String(),
		GRPC:    cfg.Server.GRPCAdvertisedAddress,
		JWKSURI: cfg.Server.BaseURL.String() + JWKSPath,
	}}

	if cfg.Federation.TLSCertFile == "" {
		return document, nil
	}

	pin, err := Fingerprint(cfg.Federation.TLSCertFile)
	if err != nil {
		return ServerDocument{}, err
	}

	document.Server.CertificateFingerprint = pin

	return document, nil
}

// Serve returns the options that mount both documents on the node's HTTP
// server. They are rendered once here, because neither changes while the
// process runs.
func Serve(cfg *config.Config) ([]httpx.Option, error) {
	server, err := NewServerDocument(cfg)
	if err != nil {
		return nil, err
	}

	clientHandler, err := handler(NewClientDocument(&cfg.Server))
	if err != nil {
		return nil, err
	}

	serverHandler, err := handler(server)
	if err != nil {
		return nil, err
	}

	return []httpx.Option{
		httpx.WithHandler("GET "+ClientPath, clientHandler),
		httpx.WithHandler("GET "+ServerPath, serverHandler),
	}, nil
}

// handler renders document once and serves those bytes.
func handler(document any) (http.Handler, error) {
	body, err := json.Marshal(document)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal,
			"the discovery document could not be rendered").WithOp(opServe)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(documentMaxAge))

		// The document is public by construction — it exists so that a party
		// with no relationship to this node can read it — and a reader's
		// application may well be a web page on another origin, which is why
		// Matrix requires the same header on its own.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		_, _ = w.Write(body)
	}), nil
}
