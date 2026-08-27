package wellknown_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// newKey returns a key to issue test certificates with.
func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	return key
}

// issue writes a certificate for key to a PEM file and returns its path. The
// serial and the validity differ on every call, which is what makes two calls
// with the same key stand for a certificate and its renewal.
func issue(t *testing.T, key *ecdsa.PrivateKey, serial int64) string {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "quire-a.example"},
		NotBefore:    time.Now().Add(time.Duration(serial) * time.Hour),
		NotAfter:     time.Now().Add(time.Duration(serial)*time.Hour + 24*time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("issuing a certificate: %v", err)
	}

	path := filepath.Join(t.TempDir(), "tls.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the certificate: %v", err)
	}

	return path
}

// This is C12 as a test. A pin taken over the certificate would differ between
// these two, and node-to-node replication would break at the first renewal.
func TestFingerprintSurvivesARenewalOfTheCertificate(t *testing.T) {
	t.Parallel()

	key := newKey(t)

	issued, err := wellknown.Fingerprint(issue(t, key, 1))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	renewed, err := wellknown.Fingerprint(issue(t, key, 2))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	if issued != renewed {
		t.Errorf("the pin changed on renewal: %s became %s", issued, renewed)
	}
}

func TestFingerprintChangesWhenTheKeyDoes(t *testing.T) {
	t.Parallel()

	first, err := wellknown.Fingerprint(issue(t, newKey(t), 1))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	second, err := wellknown.Fingerprint(issue(t, newKey(t), 1))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	if first == second {
		t.Error("two different keys produced the same pin")
	}
}

// The published value has to be the one an operator reproduces with openssl,
// or nobody can check it by hand.
func TestFingerprintIsTheDigestOfThePublicKey(t *testing.T) {
	t.Parallel()

	key := newKey(t)

	pin, err := wellknown.Fingerprint(issue(t, key, 1))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshalling the public key: %v", err)
	}

	digest := sha256.Sum256(spki)
	want := "spki-sha256:" + base64.StdEncoding.EncodeToString(digest[:])

	if pin != want {
		t.Errorf("the pin is %s, want %s", pin, want)
	}
}

func TestFingerprintRefusesWhatIsNotACertificate(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()

	notPEM := filepath.Join(directory, "garbage.crt")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	aKey := filepath.Join(directory, "tls.key")
	if err := os.WriteFile(aKey,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	cases := map[string]string{
		"a file that is not there":               filepath.Join(directory, "absent.crt"),
		"a file that is not PEM":                 notPEM,
		"a key where a certificate was expected": aKey,
	}

	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pin, err := wellknown.Fingerprint(path)
			if err == nil {
				t.Fatalf("Fingerprint succeeded with %q", pin)
			}

			if !errors.Is(err, errs.KindFailedPrecondition) {
				t.Errorf("Fingerprint failed with %v, want a %s error", err, errs.KindFailedPrecondition)
			}
		})
	}
}
