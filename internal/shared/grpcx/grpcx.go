// Package grpcx builds the gRPC surface of the node: the listener, the
// interceptor chain every method passes through, and the shutdown that lets an
// in-flight synchronization finish.
//
// The chain is the part worth stating explicitly, because the order is not a
// matter of taste. From the outermost interceptor to the one nearest the
// handler:
//
//	metrics      counts the call and times it, so that a call rejected by any
//	             interceptor below is still counted
//	request      stamps the request identifier and the peer into the context,
//	             so that everything logged below carries them
//	error        translates the domain error into the status the client sees
//	logging      reports the finished call
//	recovery     turns a panic into that same domain error
//
// Recovery sits nearest the handler on purpose. A panic caught there becomes
// an ordinary error on the way out, so the interceptors above it see a call
// that failed with Internal rather than a call that never returned: it is
// logged once, counted once, and answered once. Recovery placed outermost
// would swallow the panic before any of them ran.
//
// Logging sits *inside* the translation for the same reason and in the other
// direction. The status the client receives deliberately carries no cause —
// the text of a driver error names tables, hosts and identifiers — so an
// interceptor that only ever saw the status could not write a log record worth
// reading. Placed under the translation it still sees the domain error: its
// kind, the operation that failed, and the cause. What it loses is the gRPC
// code, and it gets that back from [CodeOf], the same function the translation
// itself uses, so the code in the log record and the code on the wire cannot
// disagree.
//
// Nothing here knows what a user is. Authentication is an interceptor like the
// others and is added by the identity slice, which is the only place that can
// verify a token.
package grpcx
