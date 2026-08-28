// Package peerauthn is how the node recognizes another node: the credentials
// its listener presents, and the pin it reads off a caller that presented one
// of its own.
//
// It is the peer-facing counterpart of internal/identity/infra/grpc/authn, and
// it lives here for the same reason that one lives in the identity slice. The
// sync slice is the only part of the node with a call whose caller is a peer,
// and the certificate is the only credential that call carries — RNF08 rather
// than RNF11, because a certificate identifies a node and a JWT identifies a
// reader, and ReplicateOperations is addressed to a node about a reader it
// replicates.
//
// # Why the client certificate is requested and not required
//
// One listener serves devices and peers. A device authenticates with a token
// and has no certificate of its own, so a listener that required one would
// refuse every device in order to identify the handful of nodes. It is
// therefore requested, and the one method that needs it is the one that
// refuses a caller without it — which is also the only place where the absence
// means anything.
//
// # What the pin is over
//
// The digest covers the SubjectPublicKeyInfo, as it does everywhere else in
// this node, and C12 in docs/tcc-corrections.md is why: a digest of the
// certificate stops matching at the first renewal. The value read here is
// compared against what the catalogue learned from the peer's own discovery
// document, so the two ends of the exchange are pinning the same bytes.
package peerauthn

import (
	"context"
	"crypto/tls"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// opAuthenticate is the operation reported by this file, in the form the errs
// package expects.
const opAuthenticate = "sync/peerauthn: authenticate"

// The stable machine-readable codes this package raises.
const (
	// CodeNoCertificate is a peer-facing call from a caller that presented no
	// certificate, which is what every device looks like.
	CodeNoCertificate = "no_peer_certificate"
	// CodeNotTLS is the same call arriving over a connection with no TLS at
	// all, which outside development means the listener was misconfigured.
	CodeNotTLS = "peer_connection_not_tls"
)

// ServerCredentials are what the node's listener presents, and nil when this
// deployment has no certificate of its own.
//
// A nil is not a failure. The development profile runs without one, the
// discovery document then publishes no pin, and a peer that reads that document
// refuses to replicate to this node unless it too was told to accept it — which
// is the same switch on both sides and the configuration refuses it in
// production.
func ServerCredentials(cfg *config.Federation) (credentials.TransportCredentials, error) {
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return nil, nil //nolint:nilnil // no certificate is a state, and the caller checks for it.
	}

	certificate, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindFailedPrecondition,
			"the node certificate could not be loaded").WithOp(opAuthenticate)
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13,

		// Requested and not required, because one listener serves devices and
		// peers: a device authenticates with a token and carries no
		// certificate, and requiring one would refuse every device in order to
		// identify a handful of nodes.
		//
		// Nothing is verified against a certificate authority either, and that
		// is the same argument the outbound half makes: two nodes belong to two
		// operators and share no authority, so what identifies a peer is the
		// pin it published and not a chain anybody could obtain.
		ClientAuth: tls.RequestClientCert,
	}), nil
}

// Require returns the pin of the certificate the caller presented, or the error
// a peer-facing handler should pass on when there is none.
//
// The two refusals are deliberately different. A caller that presented no
// certificate is a caller this method is not for — a device, most likely,
// calling the one method that is not addressed to it — and is told so. A
// connection with no TLS underneath it is this node's own misconfiguration, and
// saying that plainly is what stops an operator from looking for the fault at
// the other end.
func Require(ctx context.Context) (string, error) {
	caller, ok := peer.FromContext(ctx)
	if !ok {
		return "", errs.New(errs.KindInternal, "the node could not identify the caller").
			WithOp(opAuthenticate)
	}

	handshake, ok := caller.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errs.New(errs.KindPermissionDenied,
			"this call is served only over a secured connection").
			WithOp(opAuthenticate).
			WithCode(CodeNotTLS)
	}

	presented := handshake.State.PeerCertificates
	if len(presented) == 0 {
		return "", errs.New(errs.KindPermissionDenied,
			"this call is addressed to a node and needs its certificate").
			WithOp(opAuthenticate).
			WithCode(CodeNoCertificate)
	}

	return wellknown.FingerprintOf(presented[0]), nil
}
