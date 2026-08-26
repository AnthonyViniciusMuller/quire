package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// record decodes the single JSON log record written to buf.
func record(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}

	if strings.Contains(line, "\n") {
		t.Fatalf("expected a single record, got:\n%s", line)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("record is not valid JSON: %v\n%s", err, line)
	}

	return decoded
}

// jsonLogger builds a JSON logger writing into the returned buffer.
func jsonLogger(t *testing.T, cfg config.Log) (*slog.Logger, *bytes.Buffer) {
	t.Helper()

	buf := &bytes.Buffer{}

	return logging.New(cfg, buf), buf
}

func TestNewWritesJSONRecords(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})
	logger.Info("node started", slog.String("peer", "quire-b.example"))

	decoded := record(t, buf)

	if got, want := decoded["msg"], "node started"; got != want {
		t.Errorf("msg = %v, want %v", got, want)
	}

	if got, want := decoded["level"], "INFO"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}

	if got, want := decoded["peer"], "quire-b.example"; got != want {
		t.Errorf("peer = %v, want %v", got, want)
	}
}

func TestNewWritesTextRecords(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := logging.New(config.Log{Level: slog.LevelInfo, Format: config.LogFormatText}, buf)
	logger.Info("node started")

	if got := buf.String(); !strings.Contains(got, "msg=\"node started\"") {
		t.Errorf("text record = %q, want it to contain the message", got)
	}
}

func TestNewHonoursTheConfiguredLevel(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelWarn, Format: config.LogFormatJSON})
	logger.Info("filtered out")

	if buf.Len() != 0 {
		t.Errorf("a record below the configured level was written: %s", buf)
	}

	logger.Warn("kept")

	if got := record(t, buf)["msg"]; got != "kept" {
		t.Errorf("msg = %v, want kept", got)
	}
}

func TestNewShortensTheSourcePath(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{
		Level:     slog.LevelInfo,
		Format:    config.LogFormatJSON,
		AddSource: true,
	})
	logger.Info("with source")

	source, ok := record(t, buf)["source"].(map[string]any)
	if !ok {
		t.Fatal("no source in the record")
	}

	// The absolute build path differs between a laptop, the CI runner and the
	// container image, which makes it useless for grouping records.
	file, _ := source["file"].(string)
	if got, want := file, "logging/logging_test.go"; got != want {
		t.Errorf("source.file = %q, want %q", got, want)
	}
}

func TestContextAttributesReachEveryRecord(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})

	ctx := logging.WithAttrs(context.Background(),
		slog.String(logging.KeyRequestID, "req-1"),
		slog.String(logging.KeyDeviceID, "dev-7"))

	logger.InfoContext(ctx, "handled")

	decoded := record(t, buf)

	if got, want := decoded[logging.KeyRequestID], "req-1"; got != want {
		t.Errorf("request_id = %v, want %v", got, want)
	}

	if got, want := decoded[logging.KeyDeviceID], "dev-7"; got != want {
		t.Errorf("device_id = %v, want %v", got, want)
	}
}

func TestContextAttributesSurviveADerivedLogger(t *testing.T) {
	t.Parallel()

	// A decorating handler that forgets to re-wrap itself in WithAttrs or
	// WithGroup keeps working until the first component derives a logger, and
	// then silently stops. This is the regression guard for that.
	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})
	derived := logger.With(slog.String("component", "sync"))

	ctx := logging.WithAttrs(context.Background(), slog.String(logging.KeyRequestID, "req-2"))
	derived.InfoContext(ctx, "handled")

	decoded := record(t, buf)

	if got, want := decoded["component"], "sync"; got != want {
		t.Errorf("component = %v, want %v", got, want)
	}

	if got, want := decoded[logging.KeyRequestID], "req-2"; got != want {
		t.Errorf("request_id = %v, want %v: the context handler was dropped by With", got, want)
	}
}

func TestContextAttributesStayAtTheRootOfAGroupedLogger(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})
	grouped := logger.WithGroup("rpc")

	ctx := logging.WithAttrs(context.Background(), slog.String(logging.KeyRequestID, "req-3"))
	grouped.InfoContext(ctx, "handled", slog.String("method", "Sync"))

	decoded := record(t, buf)

	// The component's own attribute is namespaced, as it asked to be.
	group, ok := decoded["rpc"].(map[string]any)
	if !ok {
		t.Fatalf("no rpc group in the record: %v", decoded)
	}

	if got, want := group["method"], "Sync"; got != want {
		t.Errorf("rpc.method = %v, want %v", got, want)
	}

	// The request identifier must not be. Buried under rpc.request_id here and
	// under sync.request_id elsewhere, it could no longer be queried as one
	// field, and correlating a device across two nodes would break.
	if got, want := decoded[logging.KeyRequestID], "req-3"; got != want {
		t.Errorf("request_id = %v, want %v at the root of the record: %v", got, want, decoded)
	}
}

func TestWithAttrsAccumulates(t *testing.T) {
	t.Parallel()

	ctx := logging.WithAttrs(context.Background(), slog.String("a", "1"))
	ctx = logging.WithAttrs(ctx, slog.String("b", "2"))

	if got, want := len(logging.Attrs(ctx)), 2; got != want {
		t.Errorf("Attrs() has %d entries, want %d", got, want)
	}
}

func TestWithAttrsDoesNotShareItsBackingArray(t *testing.T) {
	t.Parallel()

	// Two requests deriving from the same parent context must not race on a
	// shared backing array, where one could overwrite the other's attribute.
	parent := logging.WithAttrs(context.Background(), slog.String(logging.KeyRequestID, "shared"))

	var wait sync.WaitGroup

	// One slot per goroutine below.
	results := make([]string, 2)

	for i, value := range []string{"first", "second"} {
		wait.Add(1)

		go func() {
			defer wait.Done()

			child := logging.WithAttrs(parent, slog.String(logging.KeyDeviceID, value))

			for _, attr := range logging.Attrs(child) {
				if attr.Key == logging.KeyDeviceID {
					results[i] = attr.Value.String()
				}
			}
		}()
	}

	wait.Wait()

	if results[0] != "first" || results[1] != "second" {
		t.Errorf("attributes leaked between siblings: %v", results)
	}
}

func TestAttrsOnAnUntouchedContext(t *testing.T) {
	t.Parallel()

	if got := logging.Attrs(context.Background()); got != nil {
		t.Errorf("Attrs() = %v, want nil", got)
	}
}

func TestIntoAndFrom(t *testing.T) {
	t.Parallel()

	logger, _ := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})

	if got := logging.From(logging.Into(context.Background(), logger)); got != logger {
		t.Error("From did not return the logger put in by Into")
	}

	// From never returns nil, so no caller has to guard the call.
	if got := logging.From(context.Background()); got == nil {
		t.Error("From returned nil for a context without a logger")
	}

	if got := logging.From(logging.Into(context.Background(), nil)); got == nil {
		t.Error("Into accepted a nil logger and From then returned nil")
	}
}

func TestErrExpandsADomainError(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})

	domain := errs.Wrap(errors.New("duplicate key"), errs.KindAlreadyExists, "email already registered").
		WithOp("identity/user: register").
		WithCode("email_taken")

	logger.Error("could not register", logging.Err(domain))

	group, ok := record(t, buf)[logging.KeyError].(map[string]any)
	if !ok {
		t.Fatal("the domain error did not expand into queryable fields")
	}

	for key, want := range map[string]any{
		"kind":    "already exists",
		"op":      "identity/user: register",
		"code":    "email_taken",
		"message": "email already registered",
		"cause":   "duplicate key",
	} {
		if got := group[key]; got != want {
			t.Errorf("error.%s = %v, want %v", key, got, want)
		}
	}
}

func TestErrFlattensAForeignError(t *testing.T) {
	t.Parallel()

	logger, buf := jsonLogger(t, config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON})
	logger.Error("boom", logging.Err(errors.New("plain failure")))

	if got, want := record(t, buf)[logging.KeyError], "plain failure"; got != want {
		t.Errorf("error = %v, want %v", got, want)
	}
}

func TestDiscardWritesNothing(t *testing.T) {
	t.Parallel()

	// Nothing to assert on the output; the point is that it must not panic and
	// must not reach the default logger.
	logging.Discard().Error("ignored", logging.Err(errors.New("ignored")))
}
