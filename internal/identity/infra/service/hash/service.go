// Package hash is the bcrypt implementation of the password hashing port.
//
// bcrypt rather than argon2id, and the reason is a deployment one. bcrypt is
// parameterized by a single work factor, which the node already carries as
// QUIRE_AUTH_BCRYPT_COST; argon2id is parameterized by three, and the one that
// matters most — how much memory each hashing costs — has to be agreed with the
// memory limit of the container the node runs in, or a burst of logins is an
// out-of-memory kill. OWASP still accepts bcrypt above a work factor of ten,
// and the port is what makes the choice reversible: another algorithm is
// another package here and nothing else.
//
// What bcrypt costs in exchange is a ceiling of seventy-two bytes on a
// password, which is why the domain states that bound as its own rule.
package hash

import (
	"crypto/rand"
	"encoding/base64"
	"errors"

	"golang.org/x/crypto/bcrypt"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew    = "identity/hash: new"
	opHash   = "identity/hash: hash"
	opVerify = "identity/hash: verify"
)

// placeholderBytes is how much randomness the digest nothing matches is made
// of. It is well inside bcrypt's seventy-two bytes once base64 has widened it,
// and far past guessing.
const placeholderBytes = 32

// Service satisfies the port the use cases hold.
var _ service.HashService = (*Service)(nil)

// Service hashes and verifies passwords with bcrypt.
type Service struct {
	cost int

	// absent is a digest of a secret this process generated and then forgot,
	// computed once at the configured cost. Comparing against it takes the same
	// work as comparing against a real one, which is the whole point.
	absent string
}

// New returns a service hashing at cost.
//
// It fails when the cost is outside what bcrypt accepts. The configuration
// already refuses such a value, so reaching this is a programming error rather
// than a misconfiguration — but a constructor that returned a service which
// could not hash would move the failure to the first login.
func New(cost int) (*Service, error) {
	placeholder := make([]byte, placeholderBytes)
	if _, err := rand.Read(placeholder); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal,
			"the node could not generate the placeholder digest").WithOp(opNew)
	}

	absent, err := bcrypt.GenerateFromPassword([]byte(base64.RawStdEncoding.EncodeToString(placeholder)), cost)
	if err != nil {
		return nil, errs.Wrapf(err, errs.KindInternal,
			"the password hashing cost of %d is not usable", cost).WithOp(opNew)
	}

	return &Service{cost: cost, absent: string(absent)}, nil
}

// Hash returns the bcrypt digest of plaintext, salted by bcrypt itself: the
// salt is generated per call and travels inside the digest, so nothing here has
// to store one.
func (s *Service) Hash(plaintext string) (string, error) {
	digest, err := bcrypt.GenerateFromPassword([]byte(plaintext), s.cost)

	switch {
	case err == nil:
		return string(digest), nil
	case errors.Is(err, bcrypt.ErrPasswordTooLong):
		// The domain refuses this first, and says so in terms of the password.
		// Reaching it here means a caller hashed without validating.
		return "", errs.Wrap(err, errs.KindInvalidArgument,
			"the password is longer than this node can hash").WithOp(opHash)
	default:
		return "", errs.Wrap(err, errs.KindInternal, "the password could not be hashed").WithOp(opHash)
	}
}

// Verify reports whether plaintext produced digest.
func (s *Service) Verify(plaintext, digest string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(digest), []byte(plaintext))

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		// The ordinary outcome of a typo, and not a fault.
		return false, nil
	default:
		// A digest that is not one: a truncated column, or a row written by
		// something other than this node.
		return false, errs.Wrap(err, errs.KindInternal, "the password could not be checked").WithOp(opVerify)
	}
}

// AbsentDigest is the digest no password matches.
func (s *Service) AbsentDigest() string { return s.absent }
