//go:build e2e && kind

package e2e_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// The browser lane, which exists only in the cluster.
//
// This file is `kind` and not `e2e` because the translation is the gateway's
// and the compose federation has no gateway: there, a node is reached directly
// and a browser could not reach it at all. Everything asserted below is
// therefore about deploy/k8s/istio and not about the node, which is untouched
// by any of it — the point being that the same handler answers the same way
// through a transport it has never heard of. D10 in docs/tcc-corrections.md is
// what this covers and what it costs.

// The gRPC-Web framing, which is the whole of the protocol this file exercises.
//
// A frame is one byte of flags and four of length, big-endian, and then that
// many bytes. The flag is what says which kind: a message carries the same
// protobuf a gRPC message would, and the last frame of a response carries the
// trailers instead — the HTTP trailers a browser cannot read, moved into the
// body where it can.
const (
	frameHeaderSize   = 5
	messageFrameFlag  = 0x00
	trailerFrameFlag  = 0x80
	grpcWebContent    = "application/grpc-web+proto"
	grpcWebLoginPath  = "/quire.v1.AuthService/Login"
	browserOriginHead = "https://reader.example"

	// The document RFC 8615 puts at a well-known path, and the one a client
	// with no node yet has to be able to read.
	//
	// It is the *client* document and not the server one. They are two, and the
	// difference is who is asking: a peer reads quire/server and learns the key
	// to pin for mTLS, and a reader's application reads this and learns where to
	// dial. A browser is the second, so this is the path whose readability
	// decides whether a web client can find a node at all.
	discoveryPath = "/.well-known/quire/client"
)

// TestGRPCWebPreflightAnswersWhatABrowserAsks covers the request no gRPC client
// ever makes and every browser does.
//
// A cross-origin response exposes no header that the server did not name, and
// gRPC-Web delivers the outcome of a call as grpc-status. A preflight that
// answered without naming it would leave a browser unable to tell a call that
// failed from one that succeeded — which is a failure that looks like the
// application misbehaving rather than like the deployment being wrong, so it is
// asserted here rather than left to a frontend to discover.
func TestGRPCWebPreflightAnswersWhatABrowserAsks(t *testing.T) {
	t.Parallel()

	request, err := http.NewRequestWithContext(t.Context(),
		http.MethodOptions, nodeA.baseURL+grpcWebLoginPath, http.NoBody)
	if err != nil {
		t.Fatalf("building the preflight: %v", err)
	}

	request.Header.Set("Origin", browserOriginHead)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-grpc-web")

	response, err := nodeA.httpClient(t).Do(request)
	if err != nil {
		t.Fatalf("the preflight did not reach the gateway: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("the preflight was answered %d, want %d", response.StatusCode, http.StatusOK)
	}

	if origin := response.Header.Get("Access-Control-Allow-Origin"); origin != browserOriginHead {
		t.Errorf("the preflight allowed origin %q, want %q", origin, browserOriginHead)
	}

	// The one that a frontend cannot work around and cannot easily diagnose.
	exposed := response.Header.Get("Access-Control-Expose-Headers")
	for _, header := range []string{"grpc-status", "grpc-message"} {
		if !strings.Contains(exposed, header) {
			t.Errorf("the preflight exposes %q, which does not name %s", exposed, header)
		}
	}
}

// TestGRPCWebCarriesTheSameRefusalAsGRPC is the assertion the rest of this file
// exists for: one call, two transports, one answer.
//
// The suite's own reachable() makes this call over native gRPC and expects
// Unauthenticated, because the reader does not exist. Here it is made the way a
// browser makes it — an HTTP/1.1 POST of a framed message to a path, with no
// HTTP/2 and no trailers — and the assertion is that the status is identical.
// What that shows is that nothing along the new lane invented, swallowed or
// rewrote an outcome: the gateway translated a framing and the node answered as
// it always does.
func TestGRPCWebCarriesTheSameRefusalAsGRPC(t *testing.T) {
	t.Parallel()

	// The same request reachable() sends, and deliberately so.
	body, err := proto.Marshal(&quirev1.LoginRequest{
		LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "nobody"},
		Password: "nothing",
		Device:   &quirev1.DeviceBinding{Name: "e2e-grpc-web", Platform: "browser"},
	})
	if err != nil {
		t.Fatalf("marshalling the login: %v", err)
	}

	request, err := http.NewRequestWithContext(t.Context(),
		http.MethodPost, nodeA.baseURL+grpcWebLoginPath, bytes.NewReader(frame(messageFrameFlag, body)))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}

	request.Header.Set("Content-Type", grpcWebContent)
	request.Header.Set("X-Grpc-Web", "1")
	request.Header.Set("Origin", browserOriginHead)

	response, err := nodeA.httpClient(t).Do(request)
	if err != nil {
		t.Fatalf("the call did not reach the gateway: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	// A gRPC-Web call that reached the handler is always 200. The outcome is in
	// the trailers, which is the whole reason the framing exists — a transport
	// error would arrive as something else, and that is the case worth telling
	// apart from a refusal.
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the call was answered %d, want %d — the route did not reach the node",
			response.StatusCode, http.StatusOK)
	}

	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/grpc-web") {
		t.Fatalf("the answer is %q, which is not gRPC-Web — the filter did not run", contentType)
	}

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}

	trailers := trailersOf(t, response, payload)

	// The status arrives as its number, because that is what travels; the
	// message is the node's own and is checked only for being present, since it
	// is written for an operator and not for this test.
	raw, present := trailers["grpc-status"]
	if !present {
		t.Fatalf("the answer carries no grpc-status: trailers %v, body %s", trailers, hex.EncodeToString(payload))
	}

	code, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("the grpc-status %q is not a number: %v", raw, err)
	}

	if codes.Code(code) != codes.Unauthenticated {
		t.Errorf("the browser lane answered %s, and native gRPC answers %s for the same call",
			codes.Code(code), codes.Unauthenticated)
	}

	if trailers["grpc-message"] == "" {
		t.Error("the refusal carries no grpc-message, so a browser has nothing to show")
	}
}

// frame wraps a payload in the gRPC-Web header the protocol prefixes every one
// with.
func frame(flag byte, payload []byte) []byte {
	framed := make([]byte, frameHeaderSize+len(payload))
	framed[0] = flag
	binary.BigEndian.PutUint32(framed[1:frameHeaderSize], uint32(len(payload)))
	copy(framed[frameHeaderSize:], payload)

	return framed
}

// trailersOf reads the trailers of a gRPC-Web answer, from wherever they
// arrived.
//
// There are two places, and both are legitimate. A call that produced no
// message at all — every refusal, including this file's — may be answered
// "trailers-only", with the status in the HTTP headers; a call that produced
// one carries them in a final body frame flagged 0x80. Reading both is what
// keeps this test measuring the status rather than the shape of the answer.
func trailersOf(t *testing.T, response *http.Response, payload []byte) map[string]string {
	t.Helper()

	trailers := map[string]string{}

	if status := response.Header.Get("grpc-status"); status != "" {
		trailers["grpc-status"] = status
		trailers["grpc-message"] = response.Header.Get("grpc-message")

		return trailers
	}

	for read := 0; read+frameHeaderSize <= len(payload); {
		flag := payload[read]
		size := int(binary.BigEndian.Uint32(payload[read+1 : read+frameHeaderSize]))
		start := read + frameHeaderSize

		if start+size > len(payload) {
			t.Fatalf("a frame claims %d bytes and the answer holds %d", size, len(payload)-start)
		}

		if flag == trailerFrameFlag {
			for _, line := range strings.Split(string(payload[start:start+size]), "\r\n") {
				name, value, found := strings.Cut(line, ":")
				if !found {
					continue
				}

				trailers[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
			}
		}

		read = start + size
	}

	return trailers
}

// TestDiscoveryIsReadableByABrowser covers the document a first-run client
// fetches before it knows any node at all.
//
// The browser lane above lets a page call every method in the contract. This is
// the step before that: a reader types a domain into an empty client, and what
// tells it where to send anything is this document. DiscoverServer answers the
// same question over gRPC and cannot be used here, because an RPC has to be
// addressed to a node already known — so a discovery route without a CORS
// policy leaves a web client able to talk only to a node compiled into it.
//
// The failure it guards against is quiet in the worst way: the fetch succeeds,
// the node answers 200, and the browser refuses to hand the body to the page
// that asked. Nothing in a server log says so.
func TestDiscoveryIsReadableByABrowser(t *testing.T) {
	t.Parallel()

	request, err := http.NewRequestWithContext(t.Context(),
		http.MethodGet, nodeA.baseURL+discoveryPath, http.NoBody)
	if err != nil {
		t.Fatalf("building the lookup: %v", err)
	}

	request.Header.Set("Origin", browserOriginHead)

	response, err := nodeA.httpClient(t).Do(request)
	if err != nil {
		t.Fatalf("the lookup did not reach the gateway: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("the lookup was answered %d, want %d", response.StatusCode, http.StatusOK)
	}

	// The header the whole document's reachability rests on. Without it the
	// body below arrived and no page may look at it.
	//
	// A wildcard is what the node sends and what is wanted here. The document
	// is public and uncredentialed — the same bytes for everybody, which is the
	// point of putting it at a well-known path — so there is no cookie and no
	// bearer token for a wildcard to expose. Echoing the origin instead would
	// also be correct, and the assertion accepts either rather than pinning
	// which layer answered.
	origin := response.Header.Get("Access-Control-Allow-Origin")
	if origin != "*" && origin != browserOriginHead {
		t.Errorf("the lookup allowed origin %q, want %q or %q — a browser cannot read this document",
			origin, "*", browserOriginHead)
	}

	// That it is the real document and not an error page the gateway allowed
	// the reading of.
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the document: %v", err)
	}

	var document struct {
		Client struct {
			BaseURL string `json:"base_url"`
			GRPC    string `json:"grpc"`
		} `json:"quire.client"`
	}

	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("the document is not JSON: %v\n%s", err, body)
	}

	// The two fields the document exists to carry. A client that read it and
	// found neither would have learned nothing it can dial, which is the same
	// outcome as not being allowed to read it — reached differently.
	if document.Client.BaseURL == "" {
		t.Error("the document carries no base_url")
	}

	if document.Client.GRPC == "" {
		t.Error("the document carries no grpc authority, which is what a client dials")
	}
}
