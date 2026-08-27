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

// WithChain installs a chain on the server.
func WithChain(chain Chain) Option {
	return func(s *settings) {
		s.unary = append(s.unary, chain.Unary...)
		s.stream = append(s.stream, chain.Stream...)
	}
}
