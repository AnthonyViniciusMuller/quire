package httpx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// get runs one request against handler and returns what it answered.
// The response value itself never leaves, so that closing its body stays this
// one function's business.
func get(t *testing.T, handler http.Handler, path string) (status int, header http.Header, body string) {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))

	response := recorder.Result()
	defer func() { _ = response.Body.Close() }()

	var read bytes.Buffer
	if _, err := read.ReadFrom(response.Body); err != nil {
		t.Fatalf("reading the body: %v", err)
	}

	return response.StatusCode, response.Header, read.String()
}

func TestLivenessAnswersWithoutConsultingAnything(t *testing.T) {
	t.Parallel()

	status, header, answered := get(t, livenessHandler(), LivenessPath)

	if status != http.StatusOK {
		t.Errorf("liveness answered %d, want %d", status, http.StatusOK)
	}

	if answered != "ok\n" {
		t.Errorf("liveness answered %q", answered)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", got)
	}
}

func TestReadinessIsReadyWithNothingToProbe(t *testing.T) {
	t.Parallel()

	status, _, answered := get(t, readinessHandler(nil, logging.Discard()), ReadinessPath)

	if status != http.StatusOK {
		t.Errorf("readiness answered %d, want %d", status, http.StatusOK)
	}

	if answered != "ok\n" {
		t.Errorf("readiness answered %q", answered)
	}
}

func TestReadinessReportsEveryProbeItConsulted(t *testing.T) {
	t.Parallel()

	probes := []namedProbe{
		{name: "database", probe: func(context.Context) error { return nil }},
		{name: "object_store", probe: func(context.Context) error { return nil }},
	}

	status, _, answered := get(t, readinessHandler(probes, logging.Discard()), ReadinessPath)

	if status != http.StatusOK {
		t.Errorf("readiness answered %d, want %d", status, http.StatusOK)
	}

	if answered != "database: ok\nobject_store: ok\n" {
		t.Errorf("readiness answered %q", answered)
	}
}

func TestReadinessKeepsTheReasonOutOfTheAnswer(t *testing.T) {
	t.Parallel()

	const reason = "dial tcp 10.0.0.7:5432: connect: connection refused"

	var written bytes.Buffer

	logger := logging.New(config.Log{Level: slog.LevelDebug, Format: config.LogFormatJSON}, &written)
	probes := []namedProbe{{name: "database", probe: func(context.Context) error { return errors.New(reason) }}}

	status, _, answered := get(t, readinessHandler(probes, logger), ReadinessPath)

	if status != http.StatusServiceUnavailable {
		t.Errorf("readiness answered %d, want %d", status, http.StatusServiceUnavailable)
	}

	if answered != "database: unavailable\n" {
		t.Errorf("readiness answered %q", answered)
	}

	if strings.Contains(answered, "10.0.0.7") {
		t.Error("the answer names the host that refused the connection")
	}

	if !strings.Contains(written.String(), reason) {
		t.Errorf("the log does not carry the reason: %s", written.String())
	}
}

func TestReadinessOneFailureIsEnoughToStopTheTraffic(t *testing.T) {
	t.Parallel()

	probes := []namedProbe{
		{name: "database", probe: func(context.Context) error { return nil }},
		{name: "object_store", probe: func(context.Context) error { return errors.New("no") }},
	}

	status, _, answered := get(t, readinessHandler(probes, logging.Discard()), ReadinessPath)

	if status != http.StatusServiceUnavailable {
		t.Errorf("readiness answered %d, want %d", status, http.StatusServiceUnavailable)
	}

	if answered != "database: ok\nobject_store: unavailable\n" {
		t.Errorf("readiness answered %q", answered)
	}
}

func TestReadinessGivesAProbeADeadline(t *testing.T) {
	t.Parallel()

	var deadline time.Time

	probes := []namedProbe{{name: "database", probe: func(ctx context.Context) error {
		deadline, _ = ctx.Deadline()

		return nil
	}}}

	get(t, readinessHandler(probes, logging.Discard()), ReadinessPath)

	if deadline.IsZero() {
		t.Fatal("the probe was given no deadline, so a dependency that stopped answering would hang the check")
	}

	if remaining := time.Until(deadline); remaining > probeTimeout {
		t.Errorf("the probe was given %s, want at most %s", remaining, probeTimeout)
	}
}
