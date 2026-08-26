package errs

// Kind classifies a domain error by what the caller can do about it. It is the
// only thing the transport layer needs in order to choose a gRPC code or an
// HTTP status, which keeps this package free of any transport dependency.
//
// A Kind is itself an error, so it doubles as the target of a comparison:
//
//	if errors.Is(err, errs.KindNotFound) { ... }
//
// Comparing by kind rather than by pointer identity is what lets an error be
// wrapped as many times as the call stack requires without any layer losing
// the ability to recognize it.
type Kind uint8

// The error classes recognized across the node.
const (
	// KindUnknown is the zero value, used when no other kind was assigned.
	KindUnknown Kind = iota
	// KindInvalidArgument means the request itself is malformed. Retrying it
	// unchanged will fail again.
	KindInvalidArgument
	// KindUnauthenticated means no valid credential was presented.
	KindUnauthenticated
	// KindPermissionDenied means the credential is valid but insufficient.
	KindPermissionDenied
	// KindNotFound means the addressed entity does not exist, or is hidden
	// from this caller.
	KindNotFound
	// KindAlreadyExists means creating the entity would violate uniqueness.
	KindAlreadyExists
	// KindConflict means a concurrent write won, typically a vector clock that
	// no longer matches. The caller should reconcile and retry.
	KindConflict
	// KindFailedPrecondition means the system is in a state where the
	// operation cannot run, and retrying will not help until that changes.
	KindFailedPrecondition
	// KindResourceExhausted means a quota or a rate limit was reached.
	KindResourceExhausted
	// KindUnavailable means a dependency, such as a peer node or the object
	// store, is temporarily unreachable. Retrying with backoff may work.
	KindUnavailable
	// KindInternal means the node itself is at fault. The cause belongs in the
	// logs and never in the response.
	KindInternal
	// KindUnimplemented means the method exists but is not implemented here.
	KindUnimplemented
	// KindCanceled means the caller gave up before the operation finished.
	KindCanceled
	// KindDeadlineExceeded means the operation ran past its deadline.
	KindDeadlineExceeded
)

// String returns the name of the kind.
func (k Kind) String() string {
	switch k {
	case KindUnknown:
		return "unknown"
	case KindInvalidArgument:
		return "invalid argument"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindPermissionDenied:
		return "permission denied"
	case KindNotFound:
		return "not found"
	case KindAlreadyExists:
		return "already exists"
	case KindConflict:
		return "conflict"
	case KindFailedPrecondition:
		return "failed precondition"
	case KindResourceExhausted:
		return "resource exhausted"
	case KindUnavailable:
		return "unavailable"
	case KindInternal:
		return "internal"
	case KindUnimplemented:
		return "unimplemented"
	case KindCanceled:
		return "canceled"
	case KindDeadlineExceeded:
		return "deadline exceeded"
	default:
		return "unknown"
	}
}

// Error makes a bare Kind usable as the target of [errors.Is], and as an error
// in its own right when there is nothing else to say.
func (k Kind) Error() string { return k.String() }

// Retryable reports whether repeating the same request later could succeed
// without the caller changing anything. Sync and replication use it to decide
// between backing off and giving up.
func (k Kind) Retryable() bool {
	switch k {
	case KindUnavailable, KindResourceExhausted, KindDeadlineExceeded, KindConflict:
		return true
	case KindUnknown, KindInvalidArgument, KindUnauthenticated, KindPermissionDenied,
		KindNotFound, KindAlreadyExists, KindFailedPrecondition, KindInternal,
		KindUnimplemented, KindCanceled:
		return false
	default:
		return false
	}
}
