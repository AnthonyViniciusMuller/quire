package jwks_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/infra/jwks"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/service/token"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// serve mounts the document on a running server and returns its base URL.
func serve(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}

	auth, err := token.New(&config.Auth{
		PrivateKeyPEM:    config.Secret(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})),
		KeyID:            "signing-key-1",
		Issuer:           "https://quire-a.example",
		AccessTokenTTL:   15 * time.Minute,
		RefreshTokenTTL:  720 * time.Hour,
		PasswordResetTTL: time.Hour,
	}, "quire-a.example")
	if err != nil {
		t.Fatalf("token.New: %v", err)
	}

	server, err := httpx.New(t.Context(), &config.Server{
		HTTPAddress:     "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
	}, jwks.Serve(auth))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-served; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})

	return "http://" + server.Addr().String()
}

// TestServeAtTheAdvertisedPath is the contract between this endpoint and the
// discovery document: the server document points a peer at jwks_uri, and this
// is the path it points at (RNF11).
func TestServeAtTheAdvertisedPath(t *testing.T) {
	t.Parallel()

	address := serve(t)

	// The path the discovery document publishes, rather than a copy of it.
	advertised, err := url.JoinPath(address, wellknown.JWKSPath)
	if err != nil {
		t.Fatalf("building the url: %v", err)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, advertised, http.NoBody)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("fetching the document: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	if got := response.Header.Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type = %q, want the media type RFC 7517 registers", got)
	}

	if got := response.Header.Get("Cache-Control"); got == "" {
		t.Error("the document is served without a cache lifetime, so the mesh would fetch it per request")
	}

	// Readable by a party with no relationship to this node, which is what the
	// document is for.
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it readable from any origin", got)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the document: %v", err)
	}

	var document struct {
		Keys []map[string]any `json:"keys"`
	}

	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("the document is not a JWK set: %v", err)
	}

	if len(document.Keys) != 1 {
		t.Fatalf("the document publishes %d keys, want 1", len(document.Keys))
	}

	if _, private := document.Keys[0]["d"]; private {
		t.Fatal("the served document contains the private half of the signing key")
	}
}

// TestServeRefusesOtherMethods covers what the method pattern buys: a POST is
// answered with 405 rather than with the document.
func TestServeRefusesOtherMethods(t *testing.T) {
	t.Parallel()

	address := serve(t)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, address+wellknown.JWKSPath, http.NoBody)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("posting to the document: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", response.StatusCode)
	}
}
