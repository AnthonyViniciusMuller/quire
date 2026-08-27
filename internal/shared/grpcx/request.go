package grpcx

import (
	"context"
	"log/slog"
	"strings"
	"uuid"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// RequestIDHeader is the metadata key the request identifier travels under, in
// both directions: a caller may supply one, and the node echoes the one it
// used back in the response header.
//
// The name is the one every proxy and service mesh already understands, which
// is what makes a call traceable across the Istio gateway, this node, and the
// peer it replicates to.
const RequestIDHeader = "x-request-id"

// maxRequestIDLength bounds an identifier supplied by the caller. It is longer
// than a uuid and shorter than anything worth putting in a log line.
const maxRequestIDLength = 64

// requestIDKey addresses the request identifier in a context.
type requestIDKey struct{}

// RequestID returns the identifier of the request being served, or the empty
// string outside a request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)

	return id
}

// UnaryRequestInterceptor stamps the request identifier and the method into
// the context, and echoes the identifier back to the caller.
//
// Everything logged below it carries them, at any depth, because the node's
// logger resolves the context attributes on every record. That is what makes
// "what did this device do" a query rather than an act of archaeology — and in
// a federation it has to work across nodes, which is why an identifier
// supplied by the caller is reused rather than replaced.
func UnaryRequestInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		ctx, id := requestContext(ctx, info.FullMethod)

		// The header is best effort: a handler that has already sent one made
		// its own choice, and failing the call over a log correlation would be
		// the wrong trade.
		_ = grpc.SetHeader(ctx, metadata.Pairs(RequestIDHeader, id))

		return handler(ctx, req)
	}
}

// StreamRequestInterceptor is [UnaryRequestInterceptor] for a streaming
// method.
func StreamRequestInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, id := requestContext(stream.Context(), info.FullMethod)

		_ = stream.SetHeader(metadata.Pairs(RequestIDHeader, id))

		wrapped := middleware.WrapServerStream(stream)
		wrapped.WrappedContext = ctx

		return handler(srv, wrapped)
	}
}

// requestContext derives the context the rest of the call is served under, and
// returns the identifier it was stamped with.
func requestContext(ctx context.Context, method string) (served context.Context, id string) {
	id = inheritedRequestID(ctx)
	if id == "" {
		id = uuid.New().String()
	}

	ctx = context.WithValue(ctx, requestIDKey{}, id)

	// The method is recorded here rather than left to the logging interceptor
	// because it belongs to every record the call produces, including the ones
	// written deep inside a repository that has no idea which method it serves.
	return logging.WithAttrs(ctx,
		slog.String(logging.KeyRequestID, id),
		slog.String(logging.KeyMethod, method),
	), id
}

// inheritedRequestID returns the identifier the caller supplied, or the empty
// string when there is none this node is willing to use.
//
// The value comes from outside — a device, or a peer belonging to another
// operator — and it is written into log records and echoed in a header, so it
// is accepted only in the shape an identifier has. A control character in a
// log stream is how one line is made to look like two.
func inheritedRequestID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	values := md.Get(RequestIDHeader)
	if len(values) == 0 {
		return ""
	}

	id := strings.TrimSpace(values[0])
	if id == "" || len(id) > maxRequestIDLength {
		return ""
	}

	for _, character := range id {
		if character < '!' || character > '~' {
			return ""
		}
	}

	return id
}
