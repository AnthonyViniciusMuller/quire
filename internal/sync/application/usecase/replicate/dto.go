package replicate

// Input is nothing.
//
// The use case takes no argument because there is nobody to take one from: it
// is run by the node on a timer and not by a caller, and everything it would
// have been given — how large a batch is, how long a failed delivery waits —
// is configuration the container hands it once.
//
// It is a struct and not an absence, because the shape every use case of the
// slice has is Execute(ctx, In) (Out, error) and a behaviour that runs on a
// timer is still a behaviour of the slice. What that buys is that the worker
// holds the same interface a controller does, and that this can be run once by
// hand — from a test, or from a future maintenance command — without a timer.
type Input struct{}

// Output is what one pass of the queue did, for the log.
//
// It is counted rather than listed. A tick that offered nothing is the normal
// state of a node whose peers are up to date, and one that failed everything is
// a peer that is down; both are one line in a log, and neither is worth a slice
// of identifiers nobody will read.
type Output struct {
	// Enqueued is how many deliveries this pass discovered were owed.
	Enqueued int64

	// Servers is how many peers were owed anything at all.
	Servers int

	// Offered is how many changes were handed to a peer.
	Offered int

	// Confirmed is how many the destinations answered about, whatever they
	// answered: a refusal is a verdict, and a delivery that has been answered
	// is a delivery that is over.
	Confirmed int64

	// Failed is how many were left owed, either because the peer could not be
	// reached or because it answered about the rest and not about these.
	Failed int64
}
