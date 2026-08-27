package wellknown_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// nodeConfig is a node configured the way a production one is, minus the
// certificate, which each test decides about.
func nodeConfig(t *testing.T) *config.Config {
	t.Helper()

	base, err := url.Parse("https://quire-a.example")
	if err != nil {
		t.Fatalf("parsing the base url: %v", err)
	}

	return &config.Config{
		Server: config.Server{
			Name:                  "quire-a.example",
			BaseURL:               base,
			GRPCAddress:           "127.0.0.1:0",
			HTTPAddress:           "127.0.0.1:0",
			GRPCAdvertisedAddress: "quire-a.example:9090",
			ShutdownTimeout:       5 * time.Second,
		},
	}
}

// publish mounts the documents on a running server and returns its base URL.
func publish(t *testing.T, cfg *config.Config) string {
	t.Helper()

	options, err := wellknown.Serve(cfg)
	if err != nil {
		t.Fatalf("wellknown.Serve: %v", err)
	}

	server, err := httpx.New(t.Context(), &cfg.Server, options...)
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

// fetch reads a document and decodes it into document.
func fetch(t *testing.T, address string, document any) http.Header {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, http.NoBody)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("fetching %s: %v", address, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", address, response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", address, err)
	}

	if err := json.Unmarshal(body, document); err != nil {
		t.Fatalf("decoding %s: %v", address, err)
	}

	return response.Header
}

func TestServePublishesTheClientDocument(t *testing.T) {
	t.Parallel()

	base := publish(t, nodeConfig(t))

	var document wellknown.ClientDocument

	header := fetch(t, base+wellknown.ClientPath, &document)

	if document.Client.BaseURL != "https://quire-a.example" {
		t.Errorf("base_url is %q", document.Client.BaseURL)
	}

	// Without this an application that resolved the domain still has nowhere
	// to dial, since the API is gRPC and the document is HTTP (D06).
	if document.Client.GRPC != "quire-a.example:9090" {
		t.Errorf("grpc is %q", document.Client.GRPC)
	}

	if got := header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type is %q", got)
	}

	// A reader's application may be a web page on another origin, and the
	// document exists to be read by strangers.
	if got := header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin is %q, want *", got)
	}

	if got := header.Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control is %q", got)
	}
}

func TestServePublishesTheServerDocumentWithItsPin(t *testing.T) {
	t.Parallel()

	key := newKey(t)

	cfg := nodeConfig(t)
	cfg.Federation.TLSCertFile = issue(t, key, 1)

	base := publish(t, cfg)

	var document wellknown.ServerDocument

	fetch(t, base+wellknown.ServerPath, &document)

	if document.Server.BaseURL != "https://quire-a.example" {
		t.Errorf("base_url is %q", document.Server.BaseURL)
	}

	if document.Server.GRPC != "quire-a.example:9090" {
		t.Errorf("grpc is %q", document.Server.GRPC)
	}

	if document.Server.JWKSURI != "https://quire-a.example/.well-known/jwks.json" {
		t.Errorf("jwks_uri is %q", document.Server.JWKSURI)
	}

	want, err := wellknown.Fingerprint(cfg.Federation.TLSCertFile)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	if document.Server.CertificateFingerprint != want {
		t.Errorf("cert_fingerprint is %q, want %q", document.Server.CertificateFingerprint, want)
	}
}

func TestServerDocumentOmitsThePinWhenTheNodeHasNoCertificate(t *testing.T) {
	t.Parallel()

	base := publish(t, nodeConfig(t))

	var document wellknown.ServerDocument

	fetch(t, base+wellknown.ServerPath, &document)

	if document.Server.CertificateFingerprint != "" {
		t.Errorf("cert_fingerprint is %q, want it absent", document.Server.CertificateFingerprint)
	}
}

func TestServeReportsACertificateItCannotRead(t *testing.T) {
	t.Parallel()

	cfg := nodeConfig(t)
	cfg.Federation.TLSCertFile = t.TempDir() + "/absent.crt"

	if _, err := wellknown.Serve(cfg); err == nil {
		t.Fatal("Serve succeeded with a certificate that is not there")
	}
}

// The document a peer reads has to decode into the same type this node
// publishes, which is the reason both live in one package.
func TestTheDocumentsRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := nodeConfig(t)

	published, err := wellknown.NewServerDocument(cfg)
	if err != nil {
		t.Fatalf("NewServerDocument: %v", err)
	}

	encoded, err := json.Marshal(published)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	var read wellknown.ServerDocument
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if read != published {
		t.Errorf("the document read back as %+v, want %+v", read, published)
	}
}
