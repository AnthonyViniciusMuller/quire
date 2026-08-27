package service

import (
	"context"
	"uuid"
)

// Works answers the one question this slice asks about a work, which is the
// only question it has any business asking: may this reader see it?
//
// Everything here hangs off a work. reading.annotations and reading.progress
// both reference library.ebooks and neither references a reader, so who a mark
// or a position belongs to is a fact about the work it is attached to — and
// establishing it means reading a row this slice does not own.
//
// It is therefore a port, on the pattern the identity slice's LocalServer set:
// the use cases name what they need in their own vocabulary, and an adapter in
// internal/reading/infra/service satisfies it out of the library slice's own
// repository. What that buys is that the library's table is read through the
// library's port in both slices, that these use cases are testable without one,
// and that the coupling is a single method rather than an import of another
// slice's domain in every use case that writes a mark.
//
// It deliberately does not hand back the work. A use case here has no business
// with a title or a digest; it needs to know whether to proceed or to answer
// that there is nothing there, and a port that returned the row would invite a
// controller to render a work out of the reading service.
type Works interface {
	// Visible reports nil when the work exists, has not been tombstoned and is
	// in the reader's collection, and an error of kind errs.KindNotFound when
	// any of the three is false.
	//
	// The three are one answer on purpose. A reply that distinguished them
	// would be an oracle for which identifiers exist and whose they are, and
	// the client can do nothing different with any of them.
	Visible(ctx context.Context, ebookID, userID uuid.UUID) error
}
