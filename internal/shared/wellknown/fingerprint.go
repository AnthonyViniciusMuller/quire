package wellknown

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const opFingerprint = "shared/wellknown: fingerprint"

// PinPrefix names what the digest covers. A pin that says what it is over can
// be replaced later by one over something else without a reader having to
// guess which it is holding, and it is what makes the published value
// self-describing rather than merely a hash.
//
// It is exported because both ends of the exchange need it: this package
// writes the value into the document, and the federation slice recognizes it
// on the way back in. Two definitions of one wire constant is how a publisher
// and a reader come to disagree about a format neither of them changed.
const PinPrefix = "spki-sha256:"

// certificateBlock is the PEM label of a certificate.
const certificateBlock = "CERTIFICATE"

// Fingerprint is the pin a peer checks this node's certificate against, read
// from the PEM file at path.
//
// The digest covers the SubjectPublicKeyInfo — the public key with its
// algorithm — and not the certificate. C12 in docs/tcc-corrections.md is the
// argument: a digest of the certificate changes at every ACME renewal, so
// pinning it would break node-to-node replication about every sixty days for
// every pair of nodes, and would train an operator to clear the one alarm that
// a substituted certificate would raise.
//
// The value is the form RFC 7469 defined for the same purpose, which is also
// what makes it reproducible by hand:
//
//	openssl x509 -in cert.pem -pubkey -noout |
//	  openssl pkey -pubin -outform der |
//	  openssl dgst -sha256 -binary | openssl enc -base64
//
// Only the first certificate in the file is read. A PEM bundle lists the leaf
// first and the chain behind it, and the leaf is the one the peer will be
// presented with.
func Fingerprint(path string) (string, error) {
	// The path is configuration, set by whoever operates the node, and never
	// reaches this function from a request.
	encoded, err := os.ReadFile(path) //nolint:gosec // the path is operator configuration, not input
	if err != nil {
		return "", errs.Wrapf(err, errs.KindFailedPrecondition,
			"the node certificate could not be read").WithOp(opFingerprint)
	}

	block, _ := pem.Decode(encoded)
	if block == nil || block.Type != certificateBlock {
		return "", errs.New(errs.KindFailedPrecondition,
			"the node certificate is not a PEM-encoded certificate").WithOp(opFingerprint)
	}

	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", errs.Wrap(err, errs.KindFailedPrecondition,
			"the node certificate could not be parsed").WithOp(opFingerprint)
	}

	return FingerprintOf(certificate), nil
}

// FingerprintOf is [Fingerprint] for a certificate already parsed, which is
// what the discovery client of phase 6 holds after a handshake.
func FingerprintOf(certificate *x509.Certificate) string {
	digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)

	return PinPrefix + base64.StdEncoding.EncodeToString(digest[:])
}
