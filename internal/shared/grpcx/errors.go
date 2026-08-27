package grpcx

import (
	"context"
	"errors"
	"io"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// errorDomain is the authority the machine-readable reason of an error is
// scoped to, as google.rpc.ErrorInfo requires. It names the contract rather
// than the node, because the same reason means the same thing on every node in
// the federation.
const errorDomain = "quire.v1"

// unclassifiedMessage answers an error that reached the transport without a
// client-safe message. It says nothing on purpose: the text of an
// unclassified error routinely names a table, a host or an identifier.
const unclassifiedMessage = "the node could not handle the request"

// Code is the gRPC code that answers a domain error kind.
//
// The mapping is the whole reason internal/shared/errs carries a kind: it lets
// the domain say what went wrong without importing a transport, and it lets
// this package answer without guessing.
func Code(kind errs.Kind) codes.Code {
	switch kind {
	case errs.KindInvalidArgument:
		return codes.InvalidArgument
	case errs.KindUnauthenticated:
		return codes.Unauthenticated
	case errs.KindPermissionDenied:
		return codes.PermissionDenied
	case errs.KindNotFound:
		return codes.NotFound
	case errs.KindAlreadyExists:
		return codes.AlreadyExists
	case errs.KindConflict:
		// Aborted, not FailedPrecondition: a concurrent write lost, and the
		// caller is expected to reconcile and try again. FailedPrecondition
		// tells the caller the opposite — that retrying is pointless until
		// something else changes — which for a device draining its offline
		// queue would mean giving up on an operation it should resend.
		return codes.Aborted
	case errs.KindFailedPrecondition:
		return codes.FailedPrecondition
	case errs.KindResourceExhausted:
		return codes.ResourceExhausted
	case errs.KindUnavailable:
		return codes.Unavailable
	case errs.KindInternal:
		return codes.Internal
	case errs.KindUnimplemented:
		return codes.Unimplemented
	case errs.KindCanceled:
		return codes.Canceled
	case errs.KindDeadlineExceeded:
		return codes.DeadlineExceeded
	case errs.KindUnknown:
		return codes.Unknown
	default:
		return codes.Unknown
	}
}

// CodeOf is [Code] applied to any error, wrapped or not.
//
// An error that already carries a gRPC status keeps it. That is the deliberate
// way for a handler to answer with something the domain vocabulary cannot
// express — and it is never the way to relay a peer's failure, which the
// federation client classifies into a kind of its own before returning it. A
// peer's NotFound is not this node's NotFound.
func CodeOf(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	if grpcStatus, ok := status.FromError(err); ok {
		return grpcStatus.Code()
	}

	return Code(errs.KindOf(err))
}

// Status renders err as the answer the client receives.
//
// Three things travel: the code, the client-safe message, and the details a
// client can act on — the stable reason, and the field violations behind an
// invalid argument. The wrapped cause never travels; it stays for the log
// record the chain writes one interceptor further in.
func Status(err error) *status.Status {
	if err == nil {
		return status.New(codes.OK, "")
	}

	if grpcStatus, ok := status.FromError(err); ok {
		return grpcStatus
	}

	code := Code(errs.KindOf(err))

	message := errs.MessageOf(err)
	if message == "" {
		message = unclassifiedMessage
	}

	return withDetails(status.New(code, message), err)
}

// withDetails attaches what a client can branch on, and returns the plain
// status if the details cannot be attached — an answer without details is
// worth more than no answer at all.
func withDetails(grpcStatus *status.Status, err error) *status.Status {
	details := make([]protoadapt.MessageV1, 0, 2)

	if reason := errs.CodeOf(err); reason != "" {
		details = append(details, &errdetails.ErrorInfo{Reason: reason, Domain: errorDomain})
	}

	if fields := errs.FieldsOf(err); len(fields) > 0 {
		violations := make([]*errdetails.BadRequest_FieldViolation, 0, len(fields))
		for _, field := range fields {
			violations = append(violations, &errdetails.BadRequest_FieldViolation{
				Field:       field.Name,
				Description: field.Reason,
			})
		}

		details = append(details, &errdetails.BadRequest{FieldViolations: violations})
	}

	if len(details) == 0 {
		return grpcStatus
	}

	detailed, attachErr := grpcStatus.WithDetails(details...)
	if attachErr != nil {
		return grpcStatus
	}

	return detailed
}

// UnaryErrorInterceptor translates the error a handler returned into the
// status the client receives.
//
// It sits outside the logging interceptor and inside everything else, which is
// what lets the log record still see the domain error — its kind, its
// operation and its cause — while the client sees only what [Status] allows.
func UnaryErrorInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		response, err := handler(ctx, req)
		if err != nil {
			return nil, Status(err).Err()
		}

		return response, nil
	}
}

// StreamErrorInterceptor is [UnaryErrorInterceptor] for a streaming method.
//
// io.EOF is what a client-streaming handler reads at the end of the stream and
// is not a failure, so it is left alone rather than answered with a status.
func StreamErrorInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, stream)
		if err == nil || errors.Is(err, io.EOF) {
			return err
		}

		return Status(err).Err()
	}
}
