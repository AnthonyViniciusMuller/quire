package discovery

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// thePin is a peer's published public key digest, in the form C12 settled on.
const thePin = wellknown.PinPrefix + "Zm9vYmFyCg=="

// insecureClient is the client of a development node, which is the only
// profile in which a lookup may be made over plain HTTP — and the only one an
// httptest server can be reached in.
func insecureClient() *Service {
	return New(&config.Federation{
		DiscoveryTimeout:       2 * time.Second,
		AllowInsecureDiscovery: true,
	})
}

// serve answers the peer document path with handler, and returns the domain
// the node is reachable at.
func serve(t *testing.T, handler http.HandlerFunc) server.Domain {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(wellknown.ServerPath, handler)

	listener := httptest.NewServer(mux)
	t.Cleanup(listener.Close)

	return server.Domain(strings.TrimPrefix(listener.URL, "http://"))
}

// document answers with the document a configured node publishes.
func document(endpoint wellknown.ServerEndpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(wellknown.ServerDocument{Server: endpoint})
	}
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	domain := serve(t, document(wellknown.ServerEndpoint{
		BaseURL:                "https://quire-b.example",
		GRPC:                   "quire-b.example:9090",
		JWKSURI:                "https://quire-b.example/.well-known/jwks.json",
		CertificateFingerprint: thePin,
	}))

	descriptor, err := insecureClient().Discover(t.Context(), domain)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	switch {
	case descriptor.Domain != domain:
		t.Errorf("Domain = %q, want the authority the lookup was addressed to, %q", descriptor.Domain, domain)
	case descriptor.BaseURL != "https://quire-b.example":
		t.Errorf("BaseURL = %q", descriptor.BaseURL)
	case descriptor.GRPCAuthority != "quire-b.example:9090":
		t.Error("the address a peer is dialed at was not read, which is the whole of D06")
	case descriptor.CertificateFingerprint != thePin:
		t.Error("the pin RNF08 is checked against was not read")
	case descriptor.JWKSURI != "https://quire-b.example/.well-known/jwks.json":
		t.Error("the signing key location was not read")
	}
}

// TestDiscoverOfANodeThatPublishesOnlyAnOrigin covers a peer this node can
// describe and cannot replicate to: no pin, no keys, nowhere to dial. Refusing
// it here would turn a peer that is merely unreachable into one that cannot be
// described at all.
func TestDiscoverOfANodeThatPublishesOnlyAnOrigin(t *testing.T) {
	t.Parallel()

	domain := serve(t, document(wellknown.ServerEndpoint{BaseURL: "https://quire-b.example"}))

	descriptor, err := insecureClient().Discover(t.Context(), domain)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if !descriptor.CertificateFingerprint.IsZero() || !descriptor.GRPCAuthority.IsZero() {
		t.Error("a document that published neither a pin nor an authority produced one")
	}
}

// TestDiscoverRefusesWhatIsNotANode covers the four ways a host can answer a
// lookup without being a Quire node. They are one answer on purpose: none of
// them leaves the call with a description, and telling them apart would only
// invite a caller to retry the ones that will keep failing.
func TestDiscoverRefusesWhatIsNotANode(t *testing.T) {
	t.Parallel()

	cases := map[string]http.HandlerFunc{
		"nothing at that path": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"a node that cannot say what it is": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"a page rather than a document": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<!doctype html><title>Sign in</title>"))
		},
		"a document with no origin": document(wellknown.ServerEndpoint{GRPC: "quire-b.example:9090"}),
		"a document with a certificate digest rather than a key digest": document(wellknown.ServerEndpoint{
			BaseURL:                "https://quire-b.example",
			CertificateFingerprint: "sha256:Zm9vYmFyCg==",
		}),
		"a document whose authority carries no port": document(wellknown.ServerEndpoint{
			BaseURL: "https://quire-b.example",
			GRPC:    "quire-b.example",
		}),
	}

	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := insecureClient().Discover(t.Context(), serve(t, handler))
			if err == nil {
				t.Fatal("Discover = nil, want an error")
			}

			if !errors.Is(err, errs.KindFailedPrecondition) {
				t.Errorf("error = %v, want a failed precondition", err)
			}

			if got := errs.CodeOf(err); got != service.CodeNotAQuireServer {
				t.Errorf("code = %q, want %q", got, service.CodeNotAQuireServer)
			}
		})
	}
}

// TestDiscoverBoundsTheDocument covers what a peer could otherwise do to this
// node: answer a lookup with a body it never stops writing. The reply is
// truncated at the cap and fails to decode, which is the right outcome — a
// megabyte of JSON is not a discovery document either way.
func TestDiscoverBoundsTheDocument(t *testing.T) {
	t.Parallel()

	domain := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"quire.server":{"base_url":"https://quire-b.example","jwks_uri":"`))

		block := strings.Repeat("a", 1<<16)
		for written := 0; written < maxDocumentSize; written += len(block) {
			if _, err := w.Write([]byte(block)); err != nil {
				return
			}
		}
	})

	if _, err := insecureClient().Discover(t.Context(), domain); err == nil {
		t.Fatal("Discover of an endless document = nil, want an error")
	}
}

// TestDiscoverRefusesAnEndlessRedirect covers a lookup being walked somewhere
// nobody addressed. It is refused rather than reported unreachable: nothing
// failed, and a caller told to retry would retry a policy.
func TestDiscoverRefusesAnEndlessRedirect(t *testing.T) {
	t.Parallel()

	domain := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	})

	_, err := insecureClient().Discover(t.Context(), domain)
	if err == nil {
		t.Fatal("Discover of a redirect loop = nil, want an error")
	}

	if !errors.Is(err, errs.KindFailedPrecondition) {
		t.Errorf("error = %v, want a failed precondition rather than a retryable failure", err)
	}

	if got := errs.CodeOf(err); got != service.CodeInsecureDiscovery {
		t.Errorf("code = %q, want %q", got, service.CodeInsecureDiscovery)
	}
}

// TestDiscoverOfAHostThatDoesNotAnswer is the one failure worth retrying.
func TestDiscoverOfAHostThatDoesNotAnswer(t *testing.T) {
	t.Parallel()

	listener := httptest.NewServer(http.NewServeMux())
	domain := server.Domain(strings.TrimPrefix(listener.URL, "http://"))
	listener.Close()

	_, err := insecureClient().Discover(t.Context(), domain)
	if err == nil {
		t.Fatal("Discover of a closed listener = nil, want an error")
	}

	if !errs.Retryable(err) {
		t.Errorf("error = %v, and a peer that was simply down is worth retrying", err)
	}

	if got := errs.CodeOf(err); got != service.CodeDiscoveryUnreachable {
		t.Errorf("code = %q, want %q", got, service.CodeDiscoveryUnreachable)
	}
}

// TestDiscoverRefusesADomainThatIsNotAHost covers the input the reader typed,
// which never reaches the network.
func TestDiscoverRefusesADomainThatIsNotAHost(t *testing.T) {
	t.Parallel()

	_, err := insecureClient().Discover(t.Context(), "https://quire-b.example/nodes")
	if err == nil {
		t.Fatal("Discover of something that is not a host = nil, want an error")
	}

	if got := errs.CodeOf(err); got != server.CodeInvalidDomain {
		t.Errorf("code = %q, want %q", got, server.CodeInvalidDomain)
	}
}

// TestDocumentURLIsPredictableFromTheDomain is RFC 8615 as this node applies
// it, and the profile that decides the scheme: outside development the lookup
// is https, because the pin the document carries is worth exactly as much as
// the channel it arrived on.
func TestDocumentURLIsPredictableFromTheDomain(t *testing.T) {
	t.Parallel()

	secure := New(&config.Federation{DiscoveryTimeout: time.Second})

	if got := secure.documentURL("quire-b.example"); got != "https://quire-b.example"+wellknown.ServerPath {
		t.Errorf("documentURL = %q, want the https lookup", got)
	}

	if got := insecureClient().documentURL("127.0.0.1:8080"); !strings.HasPrefix(got, "http://127.0.0.1:8080/") {
		t.Errorf("documentURL = %q, want the plain http lookup a development node makes", got)
	}
}
