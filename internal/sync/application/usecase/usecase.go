// Package command declares the shape every use case of the sync slice has.
//
// One behaviour per package, one method to run it. A package that would need a
// second Execute is two use cases, and splitting it is what keeps a controller
// from choosing between them — the choice is the route, and the route is
// declared where the service is registered.
//
// The generic interface is what a controller holds. It never names the use
// case it calls, only the pair of types it translates between, so a controller
// cannot reach past Execute into whatever the use case was built from.
//
// This slice is the one where two transports share a behaviour. Accepting a
// batch of changes for a reader is the same work whether a device offered it
// or a peer node did; what differs is who the caller is and how they were
// authenticated, which is the controller's business and is why the use case
// takes the answer rather than the credential.
package command

import "context"

// Usecase is one behaviour of the slice: an input in the vocabulary of the
// application layer, an output in the same, and an error in the vocabulary of
// internal/shared/errs.
type Usecase[In, Out any] interface {
	Execute(ctx context.Context, input In) (Out, error)
}
