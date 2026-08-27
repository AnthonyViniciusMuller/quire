// Package jwks serves the one thing the identity slice publishes over plain
// HTTP: the public half of its signing key, under /.well-known/jwks.json.
//
// It is not gRPC because RNF11 says where the document lives, and a path under
// /.well-known is an HTTP path by RFC 8615. It is here rather than in
// internal/shared/wellknown — which names the path and points the discovery
// document at it — because this slice is the only part of the node that holds a
// signing key, and the package that publishes the key should be the one that
// has it.
//
// Who reads it: the service mesh, which RNF12 delegates JWT validation to and
// which fetches this document to do it; and any peer that has to check a token
// this node issued without asking this node.
package jwks

import (
	"net/http"
	"strconv"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// documentMaxAge is how long the document may be cached, in seconds.
//
// It is shorter than the discovery documents beside it, and deliberately: those
// describe a deployment, while this one is what a verifier checks signatures
// against. A key withdrawn after a compromise has to stop being trusted within
// this window, and five minutes is short enough to be a real bound while still
// sparing the mesh a fetch per request.
const documentMaxAge = 300

// contentType is the media type RFC 7517 registers for a key set. A verifier
// that branches on it finds what it expects, and one that does not is reading
// JSON either way.
const contentType = "application/jwk-set+json"

// Serve returns the option that mounts the document on the node's HTTP server.
func Serve(auth service.AuthService) httpx.Option {
	return httpx.WithHandler("GET "+wellknown.JWKSPath, handler(auth))
}

// handler writes the document the service rendered when it was built. Nothing
// recomputes it: the key does not change while the process runs, and rotating
// it is a redeployment.
func handler(auth service.AuthService) http.Handler {
	document := auth.JWKS()

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(documentMaxAge))

		// Public by construction — it exists so that a party with no
		// relationship to this node can verify a signature — and a reader's
		// application may well be a web page on another origin, as the
		// discovery documents beside it already allow for.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		_, _ = w.Write(document)
	})
}
