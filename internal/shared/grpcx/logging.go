package grpcx

import (
	"context"
	"log/slog"

	mwlogging "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// UnaryLoggingInterceptor writes one record per finished call.
//
// One record, not two: the middleware can also report the call as it starts,
// and a node serving a device that synchronizes every few seconds would then
// pay twice for saying the same thing. The finished record carries what the
// start record would have, plus the outcome.
//
// It sits under the error interceptor, so the error it reports is still the
// domain error — kind, operation and cause — while the client receives only
// what [Status] allows. The gRPC code it records comes from [CodeOf], the same
// function the translation uses.
func UnaryLoggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return mwlogging.UnaryServerInterceptor(loggerFor(logger), loggingOptions()...)
}

// StreamLoggingInterceptor is [UnaryLoggingInterceptor] for a streaming
// method. The record is written when the stream ends, so a synchronization
// stream open for an hour is one record and not one per message.
func StreamLoggingInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return mwlogging.StreamServerInterceptor(loggerFor(logger), loggingOptions()...)
}

// loggingOptions is what the two interceptors share.
func loggingOptions() []mwlogging.Option {
	return []mwlogging.Option{
		mwlogging.WithLogOnEvents(mwlogging.FinishCall),
		mwlogging.WithCodes(CodeOf),
		mwlogging.WithLevels(codeToLevel),
	}
}

// loggerFor adapts the node's logger to the interface the middleware logs
// through. The middleware's levels are the slog levels by construction — it
// carries a copy of them so as not to depend on slog itself — so the
// conversion is the identity and not a mapping.
//
// The context reaches the record, which is what carries the request identifier
// stamped by [UnaryRequestInterceptor] into it without this package having to
// name the field.
func loggerFor(logger *slog.Logger) mwlogging.Logger {
	return mwlogging.LoggerFunc(func(ctx context.Context, level mwlogging.Level, message string, fields ...any) {
		//nolint:sloglint // the message is the middleware's, from its own fixed set of them.
		logger.Log(ctx, slog.Level(level), message, fields...)
	})
}

// codeToLevel is how loudly a finished call is reported.
//
// The middleware's default is written for a service where a failure is an
// event. Here three outcomes are not:
//
// Aborted answers a lost concurrent write, which is the normal working of the
// reconciliation and not a fault. Reporting it as a warning would fill the log
// at exactly the moment the CRDT is doing its job.
//
// Canceled is a device that lost signal in the middle of a stream. That is a
// mobile network, and there is nothing for an operator to read.
//
// Unimplemented, in a federation, is ordinarily version skew: nodes belong to
// different operators and upgrade on their own schedule, so a peer calling a
// method this node does not serve yet is expected rather than broken. It stays
// visible as a warning because it is also what contract drift looks like.
func codeToLevel(code codes.Code) mwlogging.Level {
	switch code {
	case codes.Canceled:
		return mwlogging.LevelDebug
	case codes.OK, codes.NotFound, codes.AlreadyExists, codes.InvalidArgument,
		codes.Unauthenticated, codes.Aborted, codes.FailedPrecondition, codes.OutOfRange:
		return mwlogging.LevelInfo
	case codes.PermissionDenied, codes.ResourceExhausted, codes.Unavailable,
		codes.DeadlineExceeded, codes.Unimplemented:
		return mwlogging.LevelWarn
	case codes.Unknown, codes.Internal, codes.DataLoss:
		return mwlogging.LevelError
	default:
		return mwlogging.LevelError
	}
}
