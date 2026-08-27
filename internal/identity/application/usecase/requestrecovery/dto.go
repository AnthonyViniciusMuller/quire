package requestrecovery

// Input is the address a reader wants their recovery sent to.
type Input struct {
	// Email is the address on record. It is the only channel a reader who has
	// lost their password still has, which is why nothing else identifies them
	// here — a local name would let the call be aimed at an account whose
	// address the caller does not know.
	Email string
}

// Output is empty, and empty on purpose: the call reports the same outcome
// whether or not the address is registered here, so that it cannot be used to
// find out who is.
type Output struct{}
