package logging

import (
	"context"
	"log/slog"
	"time"

	slogctx "github.com/veqryn/slog-context"
)

// WithAttrs returns a context carrying attrs in addition to whatever was
// already recorded. Every record logged with that context, at any depth,
// carries them.
//
// The attributes are prepended, which places them at the root of the record
// even when the component doing the logging derived a grouped logger. A
// request identifier buried under sync.request_id in one component and under
// rpc.request_id in another cannot be queried as one field, which would defeat
// the purpose of recording it.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}

	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}

	return slogctx.Prepend(ctx, args...)
}

// Attrs returns the attributes recorded in ctx, or nil. The result aliases the
// context and must not be modified.
func Attrs(ctx context.Context) []slog.Attr {
	return slogctx.ExtractPrepended(ctx, time.Time{}, slog.LevelInfo, "")
}

// loggerKey addresses an explicit logger in a context.
type loggerKey struct{}

// Into returns a context carrying logger, for the components that need one
// without receiving it through their constructor.
//
// The logger is stored under a key of this package rather than through
// slogctx.NewCtx, which routes it through logr and converts it on the way in
// and on the way out. A plain context value keeps the logger the caller
// provided, handler chain and all.
func Into(ctx context.Context, logger *slog.Logger) context.Context {
	if logger == nil {
		return ctx
	}

	return context.WithValue(ctx, loggerKey{}, logger)
}

// From returns the logger carried by ctx, falling back to the default logger.
// It never returns nil, so a caller never has to guard the call.
func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}

	return slog.Default()
}
