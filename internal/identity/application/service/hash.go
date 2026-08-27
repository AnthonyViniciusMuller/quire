package service

// HashService turns a password into the digest identity.users stores, and
// checks a password against one.
//
// The port names no algorithm. What it does fix is that the digest is
// self-describing — it carries the parameters it was produced with — so that
// raising the cost does not invalidate the passwords already stored: an old
// digest keeps verifying at the cost recorded in it, and is replaced the next
// time its owner sets a password.
type HashService interface {
	// Hash returns the digest of plaintext.
	Hash(plaintext string) (string, error)

	// Verify reports whether plaintext produced digest.
	//
	// A password that does not match is (false, nil), not an error. The
	// distinction matters: a wrong password is an ordinary outcome of UC07 and
	// a malformed digest is a fault of this node, and a caller that could not
	// tell them apart would either log an alarm on every typo or answer a
	// corrupt row with "wrong password".
	Verify(plaintext, digest string) (bool, error)

	// AbsentDigest is a digest that no password matches, for the caller that
	// has none to compare against.
	//
	// It exists to make a login cost the same whether or not the reader is
	// registered here. Without it, a request for an unknown name is answered as
	// soon as the lookup misses, and one for a known name only after the
	// hashing — a difference of hundreds of milliseconds, which is an oracle
	// for whether an account exists. Local names are guessable by construction
	// (RN09 puts them in every identifier), so that oracle is worth closing.
	AbsentDigest() string
}
