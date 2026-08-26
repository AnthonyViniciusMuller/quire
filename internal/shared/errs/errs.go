// Package errs is the single error vocabulary of the node.
//
// Every layer speaks it: the domain raises errors of a [Kind], the application
// annotates them as they travel outwards, and the transport layer translates
// the kind into a gRPC code or an HTTP status. There is exactly one such
// package for the whole repository, so that an error crossing a slice boundary
// never has to be translated between competing definitions.
//
// Two rules make that work:
//
// Errors are always wrapped with %w, never replaced. A cause stays reachable
// through [errors.Is] and [errors.As] no matter how many layers annotated it.
//
// Errors are compared by kind, never by pointer identity:
//
//	if errors.Is(err, errs.KindNotFound) { ... }
//
// A pointer comparison breaks the moment somebody wraps the sentinel, which is
// precisely what every intermediate layer is supposed to do.
//
// [Error.Message] is the only text that may reach a client. The wrapped cause
// is for the logs: it routinely names tables, hosts and identifiers that a
// caller has no business seeing.
package errs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Field names a single input that failed validation, so that a client can
// point at the offending form control instead of at the request as a whole.
type Field struct {
	// Name is the field path as the client knows it, such as "email" or
	// "annotation.range.start".
	Name string
	// Reason describes the violation in terms the client can show.
	Reason string
}

// Error is a domain error: a [Kind] the transport can act on, a message the
// client may read, and an optional cause kept for the logs.
//
// The zero value is not useful; build one with [New], [Newf], [Wrap] or
// [Wrapf].
type Error struct {
	// Kind classifies the failure.
	Kind Kind
	// Op names the operation that failed, such as
	// "identity/user: register". It gives a log reader the call path without
	// the cost of a stack trace.
	Op string
	// Code is a stable machine-readable identifier, such as
	// "email_already_registered". Unlike Message it is safe for a client to
	// branch on, and it never changes with a wording fix.
	Code string
	// Message is the client-safe explanation. It must not name internal
	// hosts, tables or identifiers.
	Message string
	// Fields lists the individual validation violations, if any.
	Fields []Field

	// cause is the wrapped error. It is unexported so that it can only be
	// reached through Unwrap, which keeps it out of accidental formatting.
	cause error
}

// New returns an error of kind carrying a client-safe message.
func New(kind Kind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Newf returns an error of kind whose message is formatted. Use it only for
// text a client may read; anything sensitive belongs in a wrapped cause.
func Newf(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Wrap annotates cause with kind and a client-safe message. The cause stays
// reachable through [errors.Is] and [errors.As].
//
// Wrap never returns nil, not even for a nil cause. Returning a typed nil
// pointer would produce a non-nil error interface at the call site, which is
// the one bug this package must not introduce; wrapping a nil cause is a
// mistake, and an error that says so is easier to find than a silent one.
func Wrap(cause error, kind Kind, message string) *Error {
	return &Error{Kind: kind, Message: message, cause: cause}
}

// Wrapf is [Wrap] with a formatted message.
func Wrapf(cause error, kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...), cause: cause}
}

// WithOp records the operation that failed and returns the error, for chaining.
func (e *Error) WithOp(op string) *Error {
	if e == nil {
		return nil
	}

	e.Op = op

	return e
}

// WithCode records the stable machine-readable code and returns the error, for
// chaining.
func (e *Error) WithCode(code string) *Error {
	if e == nil {
		return nil
	}

	e.Code = code

	return e
}

// WithField records a validation violation and returns the error, for
// chaining.
func (e *Error) WithField(name, reason string) *Error {
	if e == nil {
		return nil
	}

	e.Fields = append(e.Fields, Field{Name: name, Reason: reason})

	return e
}

// Error renders the error for a log line: operation, kind, message and cause,
// skipping whichever of those is absent. It is not the text a client sees;
// that is [Error.Message].
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	// Room for the operation, the kind, the message and the cause.
	parts := make([]string, 0, 4)

	if e.Op != "" {
		parts = append(parts, e.Op)
	}

	parts = append(parts, e.Kind.String())

	if e.Message != "" {
		parts = append(parts, e.Message)
	}

	if e.cause != nil {
		parts = append(parts, e.cause.Error())
	}

	return strings.Join(parts, ": ")
}

// Unwrap returns the wrapped cause, which is what makes [errors.Is] and
// [errors.As] see through this error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.cause
}

// Is reports whether target is the kind of this error. It is what allows
// errors.Is(err, errs.KindNotFound) to hold however deeply the error is
// wrapped.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}

	kind, ok := target.(Kind)

	return ok && e.Kind == kind
}

// LogValue implements [slog.LogValuer], so that logging an error yields
// queryable attributes rather than one flat string. The cause is included
// because a log line is exactly where it belongs.
func (e *Error) LogValue() slog.Value {
	if e == nil {
		return slog.StringValue("<nil>")
	}

	// Room for the kind, the operation, the code, the message and the cause.
	attrs := make([]slog.Attr, 0, 5)
	attrs = append(attrs, slog.String("kind", e.Kind.String()))

	if e.Op != "" {
		attrs = append(attrs, slog.String("op", e.Op))
	}

	if e.Code != "" {
		attrs = append(attrs, slog.String("code", e.Code))
	}

	if e.Message != "" {
		attrs = append(attrs, slog.String("message", e.Message))
	}

	if e.cause != nil {
		attrs = append(attrs, slog.String("cause", e.cause.Error()))
	}

	return slog.GroupValue(attrs...)
}

// KindOf classifies any error, wrapped or not.
//
// It returns the kind of the outermost [Error] in the chain. A context
// cancellation or timeout is recognized even when it was never wrapped, since
// those cross every layer of a distributed system unannotated. Anything else
// is [KindUnknown]: the transport layer decides how much of an unclassified
// error to reveal.
func KindOf(err error) Kind {
	if err == nil {
		return KindUnknown
	}

	var domain *Error
	if errors.As(err, &domain) {
		return domain.Kind
	}

	switch {
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return KindDeadlineExceeded
	default:
		return KindUnknown
	}
}

// CodeOf returns the stable code of the outermost [Error] in the chain that
// carries one, or the empty string.
func CodeOf(err error) string {
	for domain := (*Error)(nil); errors.As(err, &domain); err = domain.Unwrap() {
		if domain.Code != "" {
			return domain.Code
		}
	}

	return ""
}

// MessageOf returns the client-safe message of the outermost [Error] in the
// chain that carries one, or the empty string. Callers must not fall back to
// err.Error(), which may name internals.
func MessageOf(err error) string {
	for domain := (*Error)(nil); errors.As(err, &domain); err = domain.Unwrap() {
		if domain.Message != "" {
			return domain.Message
		}
	}

	return ""
}

// FieldsOf returns the validation violations of the outermost [Error] in the
// chain that carries any, or nil.
func FieldsOf(err error) []Field {
	for domain := (*Error)(nil); errors.As(err, &domain); err = domain.Unwrap() {
		if len(domain.Fields) > 0 {
			return domain.Fields
		}
	}

	return nil
}

// Retryable reports whether err is worth retrying, per [Kind.Retryable].
func Retryable(err error) bool { return KindOf(err).Retryable() }
