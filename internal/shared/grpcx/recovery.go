package grpcx

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"google.golang.org/grpc"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const opRecover = "shared/grpcx: recover"

// panicError is the cause a recovered panic is wrapped in: the value the
// handler panicked with, and the stack it panicked at. It is a cause and never
// a message, so none of it reaches the client.
type panicError struct {
	value any
	stack []byte
}

// Error renders the panic the way a log reader needs it, value first.
func (e *panicError) Error() string { return fmt.Sprintf("panic: %v\n%s", e.value, e.stack) }

// UnaryRecoveryInterceptor turns a panic in a handler into a domain error.
//
// A panicking handler would otherwise take the whole node down with it: the
// call is served on its own goroutine, and an unrecovered panic there is not a
// failed request but a stopped process — every other device's synchronization
// interrupted by one malformed annotation.
//
// It does not log. The error it produces carries the panic and its stack as
// the wrapped cause, and travels out through the error and logging
// interceptors like any other failure, which is what keeps a failed call to
// exactly one log record — so recovery is never installed without them.
func UnaryRecoveryInterceptor() grpc.UnaryServerInterceptor {
	return recovery.UnaryServerInterceptor(recovery.WithRecoveryHandlerContext(recovered))
}

// StreamRecoveryInterceptor is [UnaryRecoveryInterceptor] for a streaming
// method.
func StreamRecoveryInterceptor() grpc.StreamServerInterceptor {
	return recovery.StreamServerInterceptor(recovery.WithRecoveryHandlerContext(recovered))
}

// recovered turns the panic into the internal error the rest of the chain
// already knows how to answer and to log. The stack is taken here, inside the
// deferred recovery, while the frames that panicked are still on it.
func recovered(_ context.Context, value any) error {
	cause := &panicError{value: value, stack: debug.Stack()}

	return errs.Wrap(cause, errs.KindInternal, unclassifiedMessage).WithOp(opRecover)
}
