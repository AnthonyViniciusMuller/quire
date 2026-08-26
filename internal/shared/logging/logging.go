// Package logging builds the structured logger of the node and carries it,
// together with the attributes describing the current request, through the
// context.
//
// Every record is emitted through [log/slog]. Two conventions make the logs of
// a federated, offline-first system usable:
//
// Request-scoped attributes travel in the context, not in a logger passed from
// hand to hand. A gRPC interceptor records the request, user and device
// identifiers once with [WithAttrs], and every record produced anywhere below
// it carries them. Correlating what one device did across two nodes is then a
// query, not an act of archaeology.
//
// Errors are logged with [Err], never with err.Error(). A domain error
// implements [slog.LogValuer] and expands into queryable fields; flattening it
// into a string would throw that away.
package logging

import (
	"io"
	"log/slog"
	"path/filepath"

	slogctx "github.com/veqryn/slog-context"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
)

// The attribute keys shared across the node. Using the constants keeps a log
// query working regardless of which package wrote the record.
const (
	// KeyError holds the error being reported.
	KeyError = "error"
	// KeyRequestID correlates every record produced while serving one request.
	KeyRequestID = "request_id"
	// KeyUserID holds the internal identifier of the authenticated user.
	KeyUserID = "user_id"
	// KeyDeviceID holds the device the request came from, which is also the
	// identity a vector clock entry is keyed by.
	KeyDeviceID = "device_id"
	// KeyPeer holds the federation domain of the peer node involved.
	KeyPeer = "peer"
	// KeyMethod holds the gRPC method or the HTTP route being served.
	KeyMethod = "method"
)

// New builds the logger described by cfg, writing to w.
//
// The returned logger resolves the context attributes recorded with
// [WithAttrs] on every record.
func New(cfg config.Log, w io.Writer) *slog.Logger {
	options := &slog.HandlerOptions{
		Level:       cfg.Level,
		AddSource:   cfg.AddSource,
		ReplaceAttr: shortenSource,
	}

	var handler slog.Handler
	switch cfg.Format {
	case config.LogFormatText:
		handler = slog.NewTextHandler(w, options)
	case config.LogFormatJSON:
		handler = slog.NewJSONHandler(w, options)
	default:
		// Validation rejects anything else long before this point; defaulting
		// to the machine-readable format is the safe reading of an
		// unrecognized value.
		handler = slog.NewJSONHandler(w, options)
	}

	// The middleware resolves the context attributes recorded with WithAttrs,
	// and keeps doing so through every logger derived with With or WithGroup.
	return slog.New(slogctx.NewHandler(handler, nil))
}

// Discard returns a logger that writes nothing, for tests and for the
// occasional component that must not log.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// Err returns the attribute under which errors are logged.
//
// A [github.com/anthonyvsmuller/quire/internal/shared/errs.Error] renders as a
// group of kind, operation, code, message and cause; any other error renders
// as its text.
func Err(err error) slog.Attr { return slog.Any(KeyError, err) }

// shortenSource trims the call site down to its package and file. The absolute
// build path is noise, and it differs between a developer machine, the CI
// runner and the container image.
func shortenSource(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) != 0 || attr.Key != slog.SourceKey {
		return attr
	}

	source, ok := attr.Value.Any().(*slog.Source)
	if !ok {
		return attr
	}

	source.File = filepath.Join(filepath.Base(filepath.Dir(source.File)), filepath.Base(source.File))

	return attr
}
