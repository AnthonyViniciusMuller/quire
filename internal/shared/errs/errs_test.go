package errs_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestKindSurvivesArbitraryWrapping(t *testing.T) {
	t.Parallel()

	// The failure this package exists to prevent: an error recognized at the
	// repository, annotated by the use case and again by the handler, and no
	// longer recognizable at the top.
	repository := errs.Wrap(sql.ErrNoRows, errs.KindNotFound, "user not found").
		WithOp("identity/user: by id").
		WithCode("user_not_found")

	usecase := fmt.Errorf("register: %w", repository)
	handler := errs.Wrap(usecase, errs.KindInternal, "could not register").WithOp("grpc: register")

	if !errors.Is(handler, errs.KindNotFound) {
		t.Error("the original kind is no longer recognizable after two wraps")
	}

	if !errors.Is(handler, sql.ErrNoRows) {
		t.Error("the original cause is no longer reachable after two wraps")
	}
}

func TestIsDistinguishesKinds(t *testing.T) {
	t.Parallel()

	err := errs.New(errs.KindNotFound, "ebook not found")

	if !errors.Is(err, errs.KindNotFound) {
		t.Error("errors.Is does not match the error's own kind")
	}

	if errors.Is(err, errs.KindAlreadyExists) {
		t.Error("errors.Is matches a kind the error does not have")
	}
}

func TestComparisonIsByKindNotByIdentity(t *testing.T) {
	t.Parallel()

	// Two independently built errors of the same kind must be interchangeable.
	// A sentinel compared by pointer would fail this.
	first := errs.New(errs.KindConflict, "vector clock diverged")
	second := errs.Wrap(errors.New("boom"), errs.KindConflict, "vector clock diverged")

	if !errors.Is(first, errs.KindConflict) || !errors.Is(second, errs.KindConflict) {
		t.Error("errors of the same kind are not interchangeable")
	}
}

func TestAsReachesTheDomainError(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("outer: %w", errs.New(errs.KindPermissionDenied, "not your library").
		WithCode("not_owner"))

	var domain *errs.Error
	if !errors.As(wrapped, &domain) {
		t.Fatal("errors.As cannot reach the domain error")
	}

	if domain.Code != "not_owner" {
		t.Errorf("Code = %q, want %q", domain.Code, "not_owner")
	}
}

func TestWrapWithoutACauseStillYieldsAUsableError(t *testing.T) {
	t.Parallel()

	// Wrap must not special-case a nil cause by returning nil: a typed nil
	// pointer becomes a non-nil error interface at the call site, which is the
	// classic Go trap. Were that special case reintroduced, errors.Is below
	// would be asked about a nil error and would answer false.
	err := errs.Wrap(nil, errs.KindInternal, "no cause")

	if !errors.Is(err, errs.KindInternal) {
		t.Fatal("Wrap with a nil cause did not produce a usable error")
	}

	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Errorf("Unwrap() = %v, want nil", unwrapped)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want errs.Kind
	}{
		"nil":                  {nil, errs.KindUnknown},
		"domain error":         {errs.New(errs.KindAlreadyExists, "taken"), errs.KindAlreadyExists},
		"wrapped domain error": {fmt.Errorf("x: %w", errs.New(errs.KindConflict, "c")), errs.KindConflict},
		"foreign error":        {errors.New("boom"), errs.KindUnknown},
		// Context errors cross every layer unannotated, so they are recognized
		// even when nobody wrapped them.
		"cancellation": {context.Canceled, errs.KindCanceled},
		"timeout":      {fmt.Errorf("dial: %w", context.DeadlineExceeded), errs.KindDeadlineExceeded},
		// An explicit kind still wins over the context error underneath it.
		"annotated cancellation": {
			errs.Wrap(context.Canceled, errs.KindUnavailable, "peer went away"),
			errs.KindUnavailable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := errs.KindOf(tt.err); got != tt.want {
				t.Errorf("KindOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccessorsReturnTheOutermostValueSet(t *testing.T) {
	t.Parallel()

	inner := errs.New(errs.KindInvalidArgument, "inner message").
		WithCode("inner_code").
		WithField("title", "must not be empty")

	// The outer error annotates without restating, which is the common case:
	// its own code, message and fields are empty.
	outer := errs.Wrap(inner, errs.KindInvalidArgument, "")

	if got, want := errs.CodeOf(outer), "inner_code"; got != want {
		t.Errorf("CodeOf() = %q, want %q", got, want)
	}

	if got, want := errs.MessageOf(outer), "inner message"; got != want {
		t.Errorf("MessageOf() = %q, want %q", got, want)
	}

	fields := errs.FieldsOf(outer)
	if len(fields) != 1 || fields[0].Name != "title" {
		t.Errorf("FieldsOf() = %+v, want one violation on title", fields)
	}
}

func TestAccessorsPreferTheOuterValueWhenItIsSet(t *testing.T) {
	t.Parallel()

	inner := errs.New(errs.KindNotFound, "inner").WithCode("inner_code")
	outer := errs.Wrap(inner, errs.KindNotFound, "outer").WithCode("outer_code")

	if got, want := errs.CodeOf(outer), "outer_code"; got != want {
		t.Errorf("CodeOf() = %q, want %q", got, want)
	}

	if got, want := errs.MessageOf(outer), "outer"; got != want {
		t.Errorf("MessageOf() = %q, want %q", got, want)
	}
}

func TestAccessorsOnForeignErrors(t *testing.T) {
	t.Parallel()

	foreign := errors.New("boom")

	if got := errs.CodeOf(foreign); got != "" {
		t.Errorf("CodeOf() = %q, want empty", got)
	}

	// A foreign error must never have its text mistaken for a client-safe
	// message: it may name a host, a table or an identifier.
	if got := errs.MessageOf(foreign); got != "" {
		t.Errorf("MessageOf() = %q, want empty", got)
	}

	if got := errs.FieldsOf(foreign); got != nil {
		t.Errorf("FieldsOf() = %+v, want nil", got)
	}
}

func TestErrorRendersOperationKindMessageAndCause(t *testing.T) {
	t.Parallel()

	err := errs.Wrap(errors.New("connection refused"), errs.KindUnavailable, "peer unreachable").
		WithOp("federation: discover")

	if got, want := err.Error(), "federation: discover: unavailable: peer unreachable: connection refused"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorOmitsTheAbsentParts(t *testing.T) {
	t.Parallel()

	if got, want := errs.New(errs.KindNotFound, "").Error(), "not found"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLogValueExposesTheCause(t *testing.T) {
	t.Parallel()

	// The cause belongs in the logs, which is the one place it is welcome.
	err := errs.Wrap(errors.New("duplicate key"), errs.KindAlreadyExists, "email already registered").
		WithOp("identity/user: register").
		WithCode("email_taken")

	logged := err.LogValue().String()

	for _, want := range []string{"already exists", "identity/user: register", "email_taken", "duplicate key"} {
		if !strings.Contains(logged, want) {
			t.Errorf("LogValue() = %q, want it to contain %q", logged, want)
		}
	}
}

func TestRetryable(t *testing.T) {
	t.Parallel()

	retryable := []errs.Kind{
		errs.KindUnavailable,
		errs.KindResourceExhausted,
		errs.KindDeadlineExceeded,
		errs.KindConflict,
	}

	for _, kind := range retryable {
		if !errs.Retryable(errs.New(kind, "")) {
			t.Errorf("%v should be retryable", kind)
		}
	}

	permanent := []errs.Kind{
		errs.KindInvalidArgument,
		errs.KindUnauthenticated,
		errs.KindPermissionDenied,
		errs.KindNotFound,
		errs.KindAlreadyExists,
		errs.KindFailedPrecondition,
		errs.KindInternal,
		errs.KindUnimplemented,
		errs.KindCanceled,
		errs.KindUnknown,
	}

	for _, kind := range permanent {
		if errs.Retryable(errs.New(kind, "")) {
			t.Errorf("%v should not be retryable", kind)
		}
	}
}

func TestEveryKindHasAName(t *testing.T) {
	t.Parallel()

	// A kind added without a String case would silently render as "unknown"
	// in every log line and every error message.
	for kind := errs.KindInvalidArgument; kind <= errs.KindDeadlineExceeded; kind++ {
		if name := kind.String(); name == "unknown" {
			t.Errorf("kind %d has no name", kind)
		}
	}
}

func TestNewfFormats(t *testing.T) {
	t.Parallel()

	err := errs.Newf(errs.KindNotFound, "ebook %q not found", "moby-dick")

	if got, want := errs.MessageOf(err), `ebook "moby-dick" not found`; got != want {
		t.Errorf("MessageOf() = %q, want %q", got, want)
	}
}

func TestBuildersToleratesANilReceiver(t *testing.T) {
	t.Parallel()

	var err *errs.Error

	if got := err.WithOp("op").WithCode("code").WithField("f", "r"); got != nil {
		t.Errorf("chaining on a nil error produced %v, want nil", got)
	}
}
