package peerauthn_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/peerauthn"
)

// The pin a peer presents is the value its own discovery document publishes,
// so the two ends of the exchange have to compute it the same way.
func TestRequireReadsThePinOfTheCertificateTheCallerPresented(t *testing.T) {
	t.Parallel()

	certificate := selfSigned(t)

	pin, err := peerauthn.Require(presenting(t, certificate))
	if err != nil {
		t.Fatalf("Require: %v", err)
	}

	if want := wellknown.FingerprintOf(certificate); pin != want {
		t.Errorf("Require = %q, want the digest the peer publishes, %q", pin, want)
	}
}

// A caller with no certificate is a caller this method is not for — a device,
// most likely, calling the one method that is not addressed to it.
func TestRequireRefusesACallerThatPresentedNoCertificate(t *testing.T) {
	t.Parallel()

	_, err := peerauthn.Require(peer.NewContext(t.Context(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{}},
	}))

	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("Require = %v, want a permission denied", err)
	}
}

// A connection with no TLS underneath it is this node's own misconfiguration,
// and saying so plainly is what stops an operator looking for the fault at the
// other end.
func TestRequireRefusesAConnectionThatIsNotSecured(t *testing.T) {
	t.Parallel()

	_, err := peerauthn.Require(peer.NewContext(t.Context(), &peer.Peer{}))
	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("Require = %v, want a permission denied", err)
	}

	// And a call that arrived through no connection at all is this node
	// failing rather than the caller.
	if _, err = peerauthn.Require(t.Context()); !errors.Is(err, errs.KindInternal) {
		t.Errorf("Require = %v, want an internal error", err)
	}
}

// The development profile runs without a certificate, and a nil is a state
// rather than a failure: the discovery document then publishes no pin, and a
// peer reading it refuses to replicate here unless it too was told to accept
// that.
func TestServerCredentialsAreAbsentWithoutACertificate(t *testing.T) {
	t.Parallel()

	got, err := peerauthn.ServerCredentials(&config.Federation{})
	if err != nil {
		t.Fatalf("ServerCredentials: %v", err)
	}

	if got != nil {
		t.Error("a deployment with no certificate was given credentials to present")
	}
}

// A key pair the process cannot read is a deployment fault, and one the node
// has to report while it is still starting.
func TestServerCredentialsRefuseACertificateItCannotRead(t *testing.T) {
	t.Parallel()

	_, err := peerauthn.ServerCredentials(&config.Federation{
		TLSCertFile: t.TempDir() + "/absent.pem",
		TLSKeyFile:  t.TempDir() + "/absent.key",
	})

	if !errors.Is(err, errs.KindFailedPrecondition) {
		t.Errorf("ServerCredentials = %v, want a failed precondition", err)
	}
}

// presenting is a context carrying a connection on which the caller presented
// certificate.
func presenting(t *testing.T, certificate *x509.Certificate) context.Context {
	t.Helper()

	return peer.NewContext(t.Context(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}},
		},
	})
}

// selfSigned is a certificate of the kind a node presents: its own, signed by
// nobody, which is the whole reason the pin exists.
func selfSigned(t *testing.T) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "quire-b.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing a certificate: %v", err)
	}

	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatalf("parsing a certificate: %v", err)
	}

	return certificate
}
