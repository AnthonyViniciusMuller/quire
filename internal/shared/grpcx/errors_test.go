package grpcx_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
)

func TestCodeAnswersEveryKind(t *testing.T) {
	t.Parallel()

	wanted := map[errs.Kind]codes.Code{
		errs.KindUnknown:            codes.Unknown,
		errs.KindInvalidArgument:    codes.InvalidArgument,
		errs.KindUnauthenticated:    codes.Unauthenticated,
		errs.KindPermissionDenied:   codes.PermissionDenied,
		errs.KindNotFound:           codes.NotFound,
		errs.KindAlreadyExists:      codes.AlreadyExists,
		errs.KindConflict:           codes.Aborted,
		errs.KindFailedPrecondition: codes.FailedPrecondition,
		errs.KindResourceExhausted:  codes.ResourceExhausted,
		errs.KindUnavailable:        codes.Unavailable,
		errs.KindInternal:           codes.Internal,
		errs.KindUnimplemented:      codes.Unimplemented,
		errs.KindCanceled:           codes.Canceled,
		errs.KindDeadlineExceeded:   codes.DeadlineExceeded,
	}

	for kind, want := range wanted {
		if got := grpcx.Code(kind); got != want {
			t.Errorf("Code(%s) is %s, want %s", kind, got, want)
		}
	}
}

func TestCodeOfClassifiesAWrappedError(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  error
		want codes.Code
	}{
		"nil":                {err: nil, want: codes.OK},
		"domain error":       {err: errs.New(errs.KindNotFound, "no such e-book"), want: codes.NotFound},
		"wrapped twice":      {err: errs.Wrap(errs.New(errs.KindConflict, "lost"), errs.KindConflict, "still lost"), want: codes.Aborted},
		"context canceled":   {err: context.Canceled, want: codes.Canceled},
		"explicit status":    {err: status.Error(codes.ResourceExhausted, "slow down"), want: codes.ResourceExhausted},
		"unclassified error": {err: errors.New("something the domain never named"), want: codes.Unknown},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := grpcx.CodeOf(testCase.err); got != testCase.want {
				t.Errorf("CodeOf is %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestStatusKeepsTheCauseOutOfTheAnswer(t *testing.T) {
	t.Parallel()

	const secret = "pq: duplicate key value violates unique constraint identity_users_email_key"

	err := errs.Wrap(errors.New(secret), errs.KindInternal, "the account could not be created").
		WithOp("identity/user: register")

	answer := grpcx.Status(err)

	if answer.Code() != codes.Internal {
		t.Errorf("code is %s, want %s", answer.Code(), codes.Internal)
	}

	if answer.Message() != "the account could not be created" {
		t.Errorf("message is %q, want the client-safe one", answer.Message())
	}

	if strings.Contains(answer.Err().Error(), "identity_users_email_key") {
		t.Error("the answer names the constraint the query violated")
	}
}

func TestStatusAnswersAnUnclassifiedErrorWithoutItsText(t *testing.T) {
	t.Parallel()

	answer := grpcx.Status(errors.New("dial tcp 10.0.0.7:5432: connect: connection refused"))

	if answer.Code() != codes.Unknown {
		t.Errorf("code is %s, want %s", answer.Code(), codes.Unknown)
	}

	if strings.Contains(answer.Message(), "10.0.0.7") {
		t.Errorf("message %q names an internal host", answer.Message())
	}
}

func TestStatusCarriesTheReasonAndTheFieldViolations(t *testing.T) {
	t.Parallel()

	err := errs.New(errs.KindInvalidArgument, "the registration is incomplete").
		WithCode("invalid_registration").
		WithField("email", "is not an address").
		WithField("local_name", "is already taken")

	details := grpcx.Status(err).Details()
	if len(details) != 2 {
		t.Fatalf("the status carries %d details, want 2", len(details))
	}

	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok {
		t.Fatalf("the first detail is %T, want *errdetails.ErrorInfo", details[0])
	}

	if info.GetReason() != "invalid_registration" {
		t.Errorf("reason is %q, want the stable code", info.GetReason())
	}

	badRequest, ok := details[1].(*errdetails.BadRequest)
	if !ok {
		t.Fatalf("the second detail is %T, want *errdetails.BadRequest", details[1])
	}

	if len(badRequest.GetFieldViolations()) != 2 {
		t.Fatalf("%d violations, want 2", len(badRequest.GetFieldViolations()))
	}

	if got := badRequest.GetFieldViolations()[0].GetField(); got != "email" {
		t.Errorf("the first violation names %q, want email", got)
	}
}

// healthStub is a service whose two methods do whatever the test needs: return an
// error, panic, or answer. It stands in for a slice handler, which none of the
// slices has yet.
type healthStub struct {
	healthpb.UnimplementedHealthServer

	err        error
	panicValue any
}

func (h *healthStub) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if h.panicValue != nil {
		panic(h.panicValue)
	}

	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, h.err
}

func (h *healthStub) Watch(*healthpb.HealthCheckRequest, healthpb.Health_WatchServer) error {
	if h.panicValue != nil {
		panic(h.panicValue)
	}

	return h.err
}

// serveHealth starts a server whose only service is that stub, behind the
// error and recovery interceptors in the order the chain uses them.
func serveHealth(t *testing.T, service *healthStub) healthpb.HealthClient {
	t.Helper()

	server, err := grpcx.New(t.Context(), serverConfig(),
		grpcx.WithUnaryInterceptors(grpcx.UnaryErrorInterceptor(), grpcx.UnaryRecoveryInterceptor()),
		grpcx.WithStreamInterceptors(grpcx.StreamErrorInterceptor(), grpcx.StreamRecoveryInterceptor()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	healthpb.RegisterHealthServer(server.Registrar(), service)

	return healthpb.NewHealthClient(serve(t, server))
}

func TestErrorInterceptorTranslatesWhatTheHandlerReturned(t *testing.T) {
	t.Parallel()

	client := serveHealth(t, &healthStub{
		err: errs.Wrap(errors.New("no rows in result set"), errs.KindNotFound, "no such e-book").
			WithCode("ebook_not_found"),
	})

	_, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{})

	answer, ok := status.FromError(err)
	if !ok {
		t.Fatalf("the client received %v, want a status", err)
	}

	if answer.Code() != codes.NotFound {
		t.Errorf("code is %s, want %s", answer.Code(), codes.NotFound)
	}

	if answer.Message() != "no such e-book" {
		t.Errorf("message is %q, want the client-safe one", answer.Message())
	}

	if strings.Contains(err.Error(), "no rows in result set") {
		t.Error("the cause reached the client")
	}
}

func TestErrorInterceptorLeavesASuccessfulCallAlone(t *testing.T) {
	t.Parallel()

	client := serveHealth(t, &healthStub{})

	response, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("status is %s, want SERVING", response.GetStatus())
	}
}

func TestRecoveryInterceptorAnswersAPanickingHandler(t *testing.T) {
	t.Parallel()

	client := serveHealth(t, &healthStub{panicValue: "the annotation range is nil"})

	_, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{})

	answer, ok := status.FromError(err)
	if !ok {
		t.Fatalf("the client received %v, want a status", err)
	}

	if answer.Code() != codes.Internal {
		t.Errorf("code is %s, want %s", answer.Code(), codes.Internal)
	}

	if strings.Contains(err.Error(), "the annotation range is nil") {
		t.Error("the panic value reached the client")
	}

	// The server survived the panic, which is the point of recovering it.
	if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{}); err == nil {
		t.Error("the second call succeeded, so the stub stopped panicking")
	}
}

func TestRecoveryInterceptorAnswersAPanickingStream(t *testing.T) {
	t.Parallel()

	client := serveHealth(t, &healthStub{panicValue: errors.New("a nil map was written to")})

	stream, err := client.Watch(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	if _, err := stream.Recv(); status.Code(err) != codes.Internal {
		t.Errorf("the stream failed with %s, want %s", status.Code(err), codes.Internal)
	}
}

func TestStreamErrorInterceptorTranslatesWhatTheHandlerReturned(t *testing.T) {
	t.Parallel()

	client := serveHealth(t, &healthStub{err: errs.New(errs.KindPermissionDenied, "this node is not authorized")})

	stream, err := client.Watch(t.Context(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	_, err = stream.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("the stream failed with %s, want %s", status.Code(err), codes.PermissionDenied)
	}
}

// The recovered error is what the logging interceptor one step out will write
// down, so it has to carry the panic and its stack as the cause.
func TestRecoveredPanicKeepsTheStackAsTheCause(t *testing.T) {
	t.Parallel()

	interceptor := grpcx.UnaryRecoveryInterceptor()
	handler := func(context.Context, any) (any, error) { panic("nil annotation range") }

	_, recovered := interceptor(t.Context(), nil, &grpc.UnaryServerInfo{}, handler)

	if !errors.Is(recovered, errs.KindInternal) {
		t.Fatalf("the recovered error is %v, want an internal one", recovered)
	}

	if !strings.Contains(recovered.Error(), "nil annotation range") {
		t.Errorf("the cause %q does not carry the panic value", recovered.Error())
	}

	if !strings.Contains(recovered.Error(), "grpcx") {
		t.Errorf("the cause %q does not carry a stack", recovered.Error())
	}
}
