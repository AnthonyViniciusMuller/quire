//go:build e2e && kind

package e2e_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/anthonyvsmuller/quire/internal/client"
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

	grpcWebWatchPath = "/quire.v1.SyncService/WatchOperations"
)

// browserIdleFor is how long the watch stream is left with nothing to say
// before the test writes something.
//
// It is the whole point of that test and the reason it is not quick. Envoy's
// own default route timeout is fifteen seconds, and Istio overrides it to zero
// on a gateway — so the deployment is correct today by inheriting a default
// this repository never wrote down. A window shorter than fifteen seconds would
// pass just as happily against a route that had acquired the Envoy default
// back, which is the regression worth catching: a browser whose stream is cut
// every fifteen seconds still works, because a reconnect costs a poll, and the
// symptom is a node that looks busy for reasons nobody can find.
const browserIdleFor = 20 * time.Second

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

// TestGRPCWebCarriesAServerStreamThatStaysOpen is the other half of D10: the
// call a browser makes instead of Sync, over the transport that made Sync
// unreachable.
//
// gRPC-Web carries a unary call and a server stream. The rest of this file
// covers the unary half; WatchOperations is the only server stream a device
// calls, and until this test nothing exercised one through the gateway at all —
// the browser lane was proven able to carry a request and an answer, and not
// able to hold a response open.
//
// Two properties, and the first is the one that is easy to lose. A watch stream
// is silent for a reader nobody is writing for, which is the common case:
// report() sends nothing when the head has not moved. So the stream has to
// survive being idle, and an intermediary that closed an idle response would
// break it in a way that looks like nothing at all — the client reconnects, the
// notification still arrives through the poll behind it, and the only trace is
// a node being reconnected to every fifteen seconds forever.
//
// The second is that it delivers: a change made by another of the reader's
// devices reaches this stream as a position, and never as an operation.
//
// # Why the reader has a work before the stream opens
//
// gRPC does not send response headers until the handler sends its first message
// or asks for them to be flushed, and this handler does neither while the log is
// empty. A browser opening a watch on an empty log therefore has a fetch that
// has not resolved rather than a stream that is idle — the two are the same
// thing on the wire and not in any client API. The test writes first so that the
// first report flushes the headers, which is what gives it a stream to then
// leave idle; a web client wanting a readable stream sooner asks for a position
// it knows is behind, exactly as this does with zero.
func TestGRPCWebCarriesAServerStreamThatStaysOpen(t *testing.T) {
	who := newReader(t, nodeA)
	// The device whose session the browser lane borrows. A real web client binds
	// itself, which is a unary call and covered above; what is under test here
	// is the stream and not how a browser came by a token.
	browser := newDevice(t, nodeA, who, "browser")
	tablet := newDevice(t, nodeA, who, "tablet")

	first := writeAWork(t, tablet, "Grande Sertão: Veredas", "grande-sertao")

	asked, err := proto.Marshal(&quirev1.WatchOperationsRequest{AfterPosition: 0})
	if err != nil {
		t.Fatalf("marshalling the watch: %v", err)
	}

	// Cancelled by the deferred call, which is what ends the stream: the node
	// holds it open until the caller hangs up, so a test that returned without
	// this would leave the request running until the suite exits.
	ctx, hangUp := context.WithCancel(t.Context())
	defer hangUp()

	request, err := http.NewRequestWithContext(ctx,
		http.MethodPost, nodeA.baseURL+grpcWebWatchPath, bytes.NewReader(frame(messageFrameFlag, asked)))
	if err != nil {
		t.Fatalf("building the watch: %v", err)
	}

	request.Header.Set("Content-Type", grpcWebContent)
	request.Header.Set("X-Grpc-Web", "1")
	request.Header.Set("Origin", browserOriginHead)
	request.Header.Set("Authorization", "Bearer "+browser.State().Session.AccessToken)

	response, err := nodeA.httpClient(t).Do(request)
	if err != nil {
		t.Fatalf("the watch did not reach the gateway: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("the watch was answered %d, want %d", response.StatusCode, http.StatusOK)
	}

	arriving := readFrames(response.Body)

	// The backlog, which is what a caller asking from zero is owed and what
	// flushed the headers this test is now reading behind.
	backlog := notification(t, arriving, "the backlog")
	if backlog <= 0 {
		t.Fatalf("the stream announced position %d for a reader who has written %s", backlog, first)
	}

	// Nothing is written for this reader now, so the stream has nothing to say.
	// What is asserted is not silence — a repeated report would be correct —
	// but that the stream is still there at the end of it.
	select {
	case arrived := <-arriving:
		if arrived.err != nil {
			t.Fatalf("the stream was cut after less than %s: %v", browserIdleFor, arrived.err)
		}

		if arrived.flag == trailerFrameFlag {
			t.Fatalf("the stream ended after less than %s, with trailers %s",
				browserIdleFor, arrived.payload)
		}
	case <-time.After(browserIdleFor):
	}

	// The change the stream exists to announce, made the way UC11 makes one.
	second := writeAWork(t, tablet, "Memórias Póstumas de Brás Cubas", "bras-cubas")

	if announced := notification(t, arriving, "the change"); announced <= backlog {
		t.Errorf("the stream announced position %d after %s was written, and it had already said %d",
			announced, second, backlog)
	}
}

// writeAWork has a device author one offline and hand it over, which is how
// UC11 makes a change and the only path that grows the log (C21).
func writeAWork(t *testing.T, appliance *device, name, contents string) uuid.UUID {
	t.Helper()

	appliance.disconnect(t)

	written, err := appliance.CreateEbook(t.Context(), &client.EbookInput{
		Title:       name,
		Author:      "Machado de Assis",
		Format:      "epub",
		ContentHash: digestOf(t, contents),
		Size:        8192,
	})
	if err != nil {
		t.Fatalf("%s writing offline: %v", appliance.name, err)
	}

	appliance.reconnect(t)
	push(t, appliance)

	return written.Target
}

// notification waits for one report from the watch stream and returns the
// position it carried.
func notification(t *testing.T, arriving <-chan watchFrame, what string) int64 {
	t.Helper()

	select {
	case arrived := <-arriving:
		if arrived.err != nil {
			t.Fatalf("the stream was cut before it announced %s: %v", what, arrived.err)
		}

		if arrived.flag == trailerFrameFlag {
			t.Fatalf("the stream ended instead of announcing %s, with trailers %s", what, arrived.payload)
		}

		var notice quirev1.WatchOperationsResponse
		if err := proto.Unmarshal(arrived.payload, &notice); err != nil {
			t.Fatalf("%s is not a WatchOperationsResponse: %v", what, err)
		}

		return notice.GetLastPosition()
	case <-time.After(browserIdleFor):
		t.Fatalf("the stream did not announce %s within %s", what, browserIdleFor)
	}

	return 0
}

// watchFrame is one frame of a response that is still open, or the reason there
// will not be another.
type watchFrame struct {
	flag    byte
	payload []byte
	err     error
}

// readFrames reads the frames of a gRPC-Web response as they arrive, rather
// than after it ends.
//
// io.ReadAll is what the unary tests above use and it cannot serve here: a watch
// stream ends when the caller hangs up, so reading it to completion is waiting
// for something the test itself has to cause. The channel is buffered because
// the goroutine outlives the test by however long it takes the closed body to
// surface as an error, and a send onto an unbuffered channel nobody is reading
// any more would leak it.
func readFrames(body io.Reader) <-chan watchFrame {
	arriving := make(chan watchFrame, 8)

	go func() {
		defer close(arriving)

		header := make([]byte, frameHeaderSize)

		for {
			if _, err := io.ReadFull(body, header); err != nil {
				arriving <- watchFrame{err: err}

				return
			}

			payload := make([]byte, binary.BigEndian.Uint32(header[1:frameHeaderSize]))
			if _, err := io.ReadFull(body, payload); err != nil {
				arriving <- watchFrame{err: err}

				return
			}

			arriving <- watchFrame{flag: header[0], payload: payload}
		}
	}()

	return arriving
}
