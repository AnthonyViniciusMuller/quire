package httpx_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/httpx"
)

// serverConfig binds to port zero on the loopback.
func serverConfig() *config.Server {
	return &config.Server{HTTPAddress: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second}
}

// serve starts server in the background and returns the base URL to call it
// at. Both are torn down when the test ends.
func serve(t *testing.T, server *httpx.Server) string {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-served; err != nil {
			t.Errorf("Serve returned %v, want nil after cancellation", err)
		}
	})

	return "http://" + server.Addr().String()
}

// call runs one request and returns the status and the body.
func call(t *testing.T, method, url string) (status int, body string) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("calling %s: %v", url, err)
	}

	defer func() { _ = response.Body.Close() }()

	read, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}

	return response.StatusCode, string(read)
}

func TestServerServesTheOperationalEndpoints(t *testing.T) {
	t.Parallel()

	server, err := httpx.New(t.Context(), serverConfig(),
		httpx.WithMetrics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("go_goroutines 7\n"))
		})),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	base := serve(t, server)

	cases := map[string]struct {
		path string
		body string
	}{
		"liveness":  {path: httpx.LivenessPath, body: "ok\n"},
		"readiness": {path: httpx.ReadinessPath, body: "ok\n"},
		"metrics":   {path: httpx.MetricsPath, body: "go_goroutines 7\n"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status, answered := call(t, http.MethodGet, base+testCase.path)
			if status != http.StatusOK {
				t.Errorf("%s answered %d, want %d", testCase.path, status, http.StatusOK)
			}

			if answered != testCase.body {
				t.Errorf("%s answered %q, want %q", testCase.path, answered, testCase.body)
			}
		})
	}
}

func TestServerRefusesWhatItDoesNotServe(t *testing.T) {
	t.Parallel()

	server, err := httpx.New(t.Context(), serverConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	base := serve(t, server)

	cases := map[string]struct {
		method string
		path   string
		status int
	}{
		"an unknown path":           {method: http.MethodGet, path: "/admin", status: http.StatusNotFound},
		"a method nobody serves":    {method: http.MethodPost, path: httpx.LivenessPath, status: http.StatusMethodNotAllowed},
		"a path served under GET":   {method: http.MethodDelete, path: httpx.ReadinessPath, status: http.StatusMethodNotAllowed},
		"a metrics endpoint absent": {method: http.MethodGet, path: httpx.MetricsPath, status: http.StatusNotFound},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if status, _ := call(t, testCase.method, base+testCase.path); status != testCase.status {
				t.Errorf("%s %s answered %d, want %d", testCase.method, testCase.path, status, testCase.status)
			}
		})
	}
}

func TestServerMountsWhatTheCallerAdded(t *testing.T) {
	t.Parallel()

	server, err := httpx.New(t.Context(), serverConfig(),
		httpx.WithHandler("GET /.well-known/quire/server", http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	base := serve(t, server)

	status, answered := call(t, http.MethodGet, base+"/.well-known/quire/server")
	if status != http.StatusOK || answered != "{}" {
		t.Errorf("the mounted handler answered %d %q", status, answered)
	}
}

func TestNewReportsAnAddressItCannotBind(t *testing.T) {
	t.Parallel()

	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port: %v", err)
	}

	defer func() { _ = taken.Close() }()

	cfg := serverConfig()
	cfg.HTTPAddress = taken.Addr().String()

	server, err := httpx.New(t.Context(), cfg)
	if err == nil {
		server.Close()
		t.Fatal("New succeeded on a port already in use")
	}

	if !errors.Is(err, errs.KindUnavailable) {
		t.Errorf("New failed with %v, want a %s error", err, errs.KindUnavailable)
	}
}

func TestServeReturnsWhenTheContextIsCanceled(t *testing.T) {
	t.Parallel()

	server, err := httpx.New(t.Context(), serverConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	cancel()

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the context was canceled")
	}

	if _, err := net.DialTimeout("tcp", server.Addr().String(), time.Second); err == nil {
		t.Error("the listener still accepts connections after the shutdown")
	}
}
