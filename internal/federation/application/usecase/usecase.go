// Package command declares the shape every use case of the federation slice
// has.
//
// One behaviour per package, one method to run it. A package that would need a
// second Execute is two use cases, and splitting it is what keeps a controller
// from choosing between them — the choice is the route, and the route is
// declared where the service is registered.
//
// The generic interface is what a controller holds. It never names the use
// case it calls, only the pair of types it translates between, so a controller
// cannot reach past Execute into whatever the use case was built from.
package command

import "context"

// Usecase is one behaviour of the slice: an input in the vocabulary of the
// application layer, an output in the same, and an error in the vocabulary of
// internal/shared/errs.
//
// The context is a parameter, as it is in every other slice. Here it carries
// two things rather than one: the transaction a use case that writes to two
// repositories needs, and the deadline of a lookup made against a peer that
// may never answer.
type Usecase[In, Out any] interface {
	Execute(ctx context.Context, input In) (Out, error)
}
