package hash_test

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/anthonyvsmuller/quire/internal/identity/infra/service/hash"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// testCost is the cheapest work factor bcrypt accepts. The tests are about the
// behaviour of the service and not about how long it takes, and the node's own
// cost of twelve would make this file take minutes.
const testCost = bcrypt.MinCost

func newService(t *testing.T, cost int) *hash.Service {
	t.Helper()

	service, err := hash.New(cost)
	if err != nil {
		t.Fatalf("hash.New(%d): %v", cost, err)
	}

	return service
}

func TestHashAndVerify(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost)
	password := "correct horse battery staple"

	digest, err := service.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if strings.Contains(digest, password) {
		t.Fatal("the digest contains the password")
	}

	matched, err := service.Verify(password, digest)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !matched {
		t.Error("the password does not verify against its own digest")
	}
}

// TestVerifyOfAWrongPasswordIsNotAnError covers the distinction the port makes:
// a typo is an ordinary outcome of UC07, and only a corrupt digest is a fault
// of this node.
func TestVerifyOfAWrongPasswordIsNotAnError(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost)

	digest, err := service.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	matched, err := service.Verify("correct horse battery stapl", digest)
	if err != nil {
		t.Fatalf("Verify of a wrong password returned an error: %v", err)
	}

	if matched {
		t.Error("a wrong password verified")
	}
}

func TestVerifyOfADigestThatIsNotOne(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost)

	matched, err := service.Verify("correct horse battery staple", "not a digest")
	if err == nil {
		t.Fatal("Verify against a malformed digest = nil, want an error")
	}

	if matched {
		t.Error("a malformed digest verified")
	}

	if !errors.Is(err, errs.KindInternal) {
		t.Errorf("error = %v, want an internal error: a row this node cannot read is its own fault", err)
	}
}

// TestHashIsSalted is what stops the table from telling an attacker which two
// readers chose the same password.
func TestHashIsSalted(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost)
	password := "correct horse battery staple"

	first, err := service.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	second, err := service.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if first == second {
		t.Error("hashing the same password twice produced the same digest, so the digests are unsalted")
	}
}

// TestHashRecordsTheCost is what makes raising the work factor safe: an old
// digest keeps verifying at the cost recorded in it rather than being
// invalidated.
func TestHashRecordsTheCost(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost+1)

	digest, err := service.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	cost, err := bcrypt.Cost([]byte(digest))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}

	if cost != testCost+1 {
		t.Errorf("the digest records a cost of %d, want %d", cost, testCost+1)
	}
}

func TestHashRejectsAPasswordBcryptCannotTake(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost)

	if _, err := service.Hash(strings.Repeat("a", 73)); err == nil {
		t.Fatal("Hash of 73 bytes = nil, want an error")
	} else if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}
}

// TestAbsentDigest is the account enumeration defence: a login for a reader who
// does not exist has to cost what a login for one who does costs, so the digest
// compared against has to be a real one at the same work factor.
func TestAbsentDigest(t *testing.T) {
	t.Parallel()

	service := newService(t, testCost)
	absent := service.AbsentDigest()

	cost, err := bcrypt.Cost([]byte(absent))
	if err != nil {
		t.Fatalf("the placeholder is not a bcrypt digest: %v", err)
	}

	if cost != testCost {
		t.Errorf("the placeholder records a cost of %d, want the configured %d", cost, testCost)
	}

	for _, guess := range []string{"", "password", "correct horse battery staple"} {
		matched, err := service.Verify(guess, absent)
		if err != nil {
			t.Fatalf("Verify against the placeholder: %v", err)
		}

		if matched {
			t.Errorf("the password %q matched the placeholder digest", guess)
		}
	}
}

// TestAbsentDigestDiffersPerProcess keeps the placeholder from becoming a value
// an attacker can recognize in a dump and use to tell the rows apart.
func TestAbsentDigestDiffersPerProcess(t *testing.T) {
	t.Parallel()

	first, second := newService(t, testCost), newService(t, testCost)

	if first.AbsentDigest() == second.AbsentDigest() {
		t.Error("two services share a placeholder digest, so it is a constant rather than a secret")
	}
}

func TestNewRejectsACostBcryptRefuses(t *testing.T) {
	t.Parallel()

	if _, err := hash.New(bcrypt.MaxCost + 1); err == nil {
		t.Fatal("hash.New above the maximum cost = nil, want an error")
	}
}
