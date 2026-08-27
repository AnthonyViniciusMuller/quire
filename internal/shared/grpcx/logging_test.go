package grpcx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// recorder is the log stream of a test, safe to read while the server that
// writes it is still running.
type recorder struct {
	mu      sync.Mutex
	written bytes.Buffer
}

func (r *recorder) Write(record []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.written.Write(record)
}

// records parses what was written, one decoded record per line.
func (r *recorder) records(t *testing.T) []map[string]any {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	var parsed []map[string]any

	for line := range strings.Lines(r.written.String()) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decoding the record %q: %v", line, err)
		}

		parsed = append(parsed, record)
	}

	return parsed
}

// only returns the single record written, failing when there is not exactly
// one — a call that logged twice is the defect this asserts against.
func (r *recorder) only(t *testing.T) map[string]any {
	t.Helper()

	written := r.records(t)
	if len(written) != 1 {
		t.Fatalf("the call wrote %d records, want 1: %v", len(written), written)
	}

	return written[0]
}

// serveChained starts a server behind the whole chain, writing its log stream
// into the recorder.
func serveChained(t *testing.T, written *recorder, service *healthStub) healthpb.HealthClient {
	t.Helper()

	logger := logging.New(config.Log{Level: slog.LevelDebug, Format: config.LogFormatJSON}, written)

	server, err := grpcx.New(t.Context(), serverConfig(), grpcx.WithChain(grpcx.NewChain(logger)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	healthpb.RegisterHealthServer(server.Registrar(), service)

	return healthpb.NewHealthClient(serve(t, server))
}

func TestLoggingInterceptorReportsAFinishedCallOnce(t *testing.T) {
	t.Parallel()

	var written recorder

	client := serveChained(t, &written, &healthStub{})

	if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	record := written.only(t)

	if record["msg"] != "finished call" {
		t.Errorf("the record says %q", record["msg"])
	}

	if record["level"] != "INFO" {
		t.Errorf("the record is at %v, want INFO", record["level"])
	}

	if record["grpc.code"] != codes.OK.String() {
		t.Errorf("grpc.code is %v, want OK", record["grpc.code"])
	}

	if record["method"] != "/grpc.health.v1.Health/Check" {
		t.Errorf("method is %v, want the full method", record["method"])
	}

	if _, ok := record[logging.KeyRequestID].(string); !ok {
		t.Errorf("the record carries no %s, so nothing correlates it", logging.KeyRequestID)
	}

	if _, ok := record["grpc.time_ms"]; !ok {
		t.Error("the record carries no duration")
	}
}

func TestLoggingInterceptorKeepsTheCauseTheClientNeverSees(t *testing.T) {
	t.Parallel()

	const cause = "no rows in result set"

	var written recorder

	client := serveChained(t, &written, &healthStub{
		err: errs.Wrap(errors.New(cause), errs.KindNotFound, "no such e-book").
			WithOp("library/ebook: get"),
	})

	_, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("the client received %v, want NotFound", err)
	}

	if strings.Contains(err.Error(), cause) {
		t.Error("the cause reached the client")
	}

	record := written.only(t)

	// The code is what the translation produced, although this interceptor
	// runs underneath it and never saw a status.
	if record["grpc.code"] != codes.NotFound.String() {
		t.Errorf("grpc.code is %v, want NotFound", record["grpc.code"])
	}

	reported, _ := record["grpc.error"].(string)
	if !strings.Contains(reported, cause) {
		t.Errorf("the record says %q, which does not carry the cause", reported)
	}

	if !strings.Contains(reported, "library/ebook: get") {
		t.Errorf("the record says %q, which does not carry the operation", reported)
	}
}

func TestLoggingInterceptorReportsARecoveredPanicWithItsStack(t *testing.T) {
	t.Parallel()

	var written recorder

	client := serveChained(t, &written, &healthStub{panicValue: "nil annotation range"})

	if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("the client received %v, want Internal", err)
	}

	record := written.only(t)

	if record["level"] != "ERROR" {
		t.Errorf("the record is at %v, want ERROR", record["level"])
	}

	reported, _ := record["grpc.error"].(string)
	if !strings.Contains(reported, "nil annotation range") {
		t.Errorf("the record says %q, which does not carry the panic", reported)
	}

	if !strings.Contains(reported, "grpcx") {
		t.Errorf("the record says %q, which does not carry a stack", reported)
	}
}

func TestLoggingInterceptorReportsAnOutcomeAsLoudlyAsItDeserves(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		kind  errs.Kind
		level string
	}{
		// A lost concurrent write is the reconciliation working, not a fault.
		"conflict":          {kind: errs.KindConflict, level: "INFO"},
		"not found":         {kind: errs.KindNotFound, level: "INFO"},
		"unauthenticated":   {kind: errs.KindUnauthenticated, level: "INFO"},
		"permission denied": {kind: errs.KindPermissionDenied, level: "WARN"},
		"unavailable":       {kind: errs.KindUnavailable, level: "WARN"},
		"internal":          {kind: errs.KindInternal, level: "ERROR"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var written recorder

			client := serveChained(t, &written, &healthStub{err: errs.New(testCase.kind, "no")})

			if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{}); err == nil {
				t.Fatal("Check succeeded")
			}

			if record := written.only(t); record["level"] != testCase.level {
				t.Errorf("a %s call is reported at %v, want %s", testCase.kind, record["level"], testCase.level)
			}
		})
	}
}
