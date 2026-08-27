package grpcx

import (
	"log/slog"

	"google.golang.org/grpc"
)

// Chain is the ordered set of interceptors every method passes through,
// outermost first. Package grpcx documents the order and the reasons for it.
type Chain struct {
	// Unary is the chain of the unary methods.
	Unary []grpc.UnaryServerInterceptor
	// Stream is the chain of the streaming methods.
	Stream []grpc.StreamServerInterceptor
}

// NewChain assembles the interceptors of the node.
//
// It exists so that the order is decided once, in one place, and so that no
// deployment can install recovery without the logging that reports what it
// recovered from.
func NewChain(logger *slog.Logger) Chain {
	return Chain{
		Unary: []grpc.UnaryServerInterceptor{
			UnaryRequestInterceptor(),
			UnaryErrorInterceptor(),
			UnaryLoggingInterceptor(logger),
			UnaryRecoveryInterceptor(),
		},
		Stream: []grpc.StreamServerInterceptor{
			StreamRequestInterceptor(),
			StreamErrorInterceptor(),
			StreamLoggingInterceptor(logger),
			StreamRecoveryInterceptor(),
		},
	}
}

// Around returns the chain with one more pair of interceptors placed before
// everything already in it.
//
// Measurement is what belongs there, and only measurement: a call rejected by
// any interceptor below still has to be counted, or the rejection is invisible
// in exactly the metric meant to show it. It is a method rather than an
// argument to [NewChain] so that this package does not have to know what a
// metric registry is.
func (c Chain) Around(unary grpc.UnaryServerInterceptor, stream grpc.StreamServerInterceptor) Chain {
	return Chain{
		Unary:  append([]grpc.UnaryServerInterceptor{unary}, c.Unary...),
		Stream: append([]grpc.StreamServerInterceptor{stream}, c.Stream...),
	}
}

// WithChain installs a chain on the server.
func WithChain(chain Chain) Option {
	return func(s *settings) {
		s.unary = append(s.unary, chain.Unary...)
		s.stream = append(s.stream, chain.Stream...)
	}
}
