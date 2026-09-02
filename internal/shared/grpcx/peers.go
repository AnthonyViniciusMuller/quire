package grpcx

import (
	"crypto/tls"
	"crypto/x509"
	"sync"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// opDial is the operation reported by this file, in the form the errs package
// expects.
const opDial = "shared/grpcx: dial peer"

// The stable machine-readable codes the dialer raises.
const (
	// CodePeerUnreachable is a node that could not be dialed at all.
	CodePeerUnreachable = "peer_unreachable"
	// CodePeerNotPinned is a node that presented a certificate whose public
	// key is not the one this node pinned for it — or none.
	CodePeerNotPinned = "peer_certificate_not_pinned"
	// CodePeerUnpinnable is a node this instance holds no pin for, outside the
	// development profile that allows dialing one anyway.
	CodePeerUnpinnable = "peer_not_pinned"
)

// PeerDialer opens the connections this node makes to other nodes, and is the
// only place where Quire is a client of Quire.
//
// It is the one client in the node that has to establish trust with a party
// it shares no root of trust with. Two nodes belong to two operators, so there
// is no certificate authority both of them answer to; what stands in for one
// is the pin each publishes in its own discovery document and the other
// stores in its catalogue (RNF08). The dialer takes the pin as an argument
// rather than reading the catalogue, because the catalogue belongs to the
// federation slice and this package knows nothing about nodes: it knows how to
// open a connection that is refused unless the far end proves it holds the
// key that pin names.
//
// # What the pin is over, and what that costs
//
// The digest covers the SubjectPublicKeyInfo and not the certificate, which is
// C12 in docs/tcc-corrections.md: a digest of the certificate stops matching
// at the first ACME renewal, so pinning it would break replication between
// every pair of nodes about every sixty days and would train an operator to
// clear the one alarm a substituted certificate would raise.
//
// Checking it means turning off the verification the library would do and
// doing another. That is not a weakening: the chain a public CA would sign is
// not evidence about *this* peer, since any certificate that CA issues chains
// equally well. The pin is evidence about this peer, and it is the only thing
// here that is.
//
// # When there is no pin
//
// A peer that published none is not dialed outside development. The
// alternative is to dial it without checking anything, which is how a pin
// becomes optional in practice: a node that talks to unpinned peers is a node
// whose pins protect nothing, because an attacker only has to serve a document
// without one. The development profile is the exception, and it says so at
// startup rather than in a comment.
//
// # One connection per node
//
// A connection is kept per node and reused, and it is replaced when the
// authority or the pin it was opened against changes: a peer that renewed
// its key or moved is dialed afresh, and one that did neither is not dialed
// on every call. Both slices that call peers share one dialer, so a node
// admitting a reader and a node being offered that reader's changes are one
// connection.
type PeerDialer struct {
	identity *tls.Certificate
	insecure bool

	mu          sync.Mutex
	connections map[uuid.UUID]*peerConnection
}

// peerConnection is one open channel and what it was opened against.
type peerConnection struct {
	channel   *grpc.ClientConn
	authority string
	pin       string
}

// NewPeerDialer returns the dialer, presenting the node's own certificate on
// every connection when the deployment has one.
//
// The certificate is what lets the far end identify this node in turn: a
// peer-facing call is authenticated by the certificate the caller presents,
// and a node with none can be offered nothing and can admit nobody.
func NewPeerDialer(cfg *config.Federation) (*PeerDialer, error) {
	dialer := &PeerDialer{
		insecure:    cfg.AllowInsecureDiscovery,
		connections: map[uuid.UUID]*peerConnection{},
	}

	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, errs.Wrap(err, errs.KindFailedPrecondition,
				"the node certificate could not be loaded").WithOp(opDial)
		}

		dialer.identity = &certificate
	}

	return dialer, nil
}

// Dial returns the connection to one node, opening it the first time and
// whenever the authority or the pin has changed since.
//
// The connection is lazy, as gRPC connections are: a node that answers
// nothing fails at the first call rather than here.
func (d *PeerDialer) Dial(id uuid.UUID, authority, pin string) (*grpc.ClientConn, error) {
	if pin == "" && !d.insecure {
		return nil, errs.New(errs.KindFailedPrecondition, "this node holds no pin for the peer").
			WithOp(opDial).
			WithCode(CodePeerUnpinnable)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if held, open := d.connections[id]; open {
		if held.authority == authority && held.pin == pin {
			return held.channel, nil
		}

		_ = held.channel.Close()
		delete(d.connections, id)
	}

	channel, err := grpc.NewClient(authority, grpc.WithTransportCredentials(d.credentials(pin)))
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "the peer could not be dialed").
			WithOp(opDial).
			WithCode(CodePeerUnreachable)
	}

	d.connections[id] = &peerConnection{channel: channel, authority: authority, pin: pin}

	return channel, nil
}

// Close releases every connection the dialer holds.
func (d *PeerDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for id, held := range d.connections {
		_ = held.channel.Close()

		delete(d.connections, id)
	}

	return nil
}

// credentials is the transport for one peer: TLS that trusts the pin and
// nothing else, or plaintext in the one profile that allows it.
func (d *PeerDialer) credentials(pin string) credentials.TransportCredentials {
	if pin == "" {
		return insecure.NewCredentials()
	}

	settings := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // replaced by the pin check below, which is narrower.
		MinVersion:         tls.VersionTLS13,
		// Checked twice, on purpose. The first runs on the raw certificates of
		// a full handshake, so that a peer with the wrong key never sees a
		// request; the second runs on every connection, resumed sessions
		// included, which the first is skipped for — a resumed session that
		// went unchecked would be a pin that held for one connection only.
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyPin(raw, pin)
		},
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyPresented(state.PeerCertificates, pin)
		},
	}

	if d.identity != nil {
		settings.Certificates = []tls.Certificate{*d.identity}
	}

	return credentials.NewTLS(settings)
}

// verifyPin checks the raw leaf the peer presented against the pin.
func verifyPin(raw [][]byte, pin string) error {
	if len(raw) == 0 {
		return errs.New(errs.KindUnavailable, "the peer presented no certificate").
			WithOp(opDial).
			WithCode(CodePeerNotPinned)
	}

	leaf, err := x509.ParseCertificate(raw[0])
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "the peer presented a certificate this node cannot read").
			WithOp(opDial).
			WithCode(CodePeerNotPinned)
	}

	return verifyPresented([]*x509.Certificate{leaf}, pin)
}

// verifyPresented checks the parsed leaf the peer presented against the pin.
func verifyPresented(presented []*x509.Certificate, pin string) error {
	if len(presented) == 0 {
		return errs.New(errs.KindUnavailable, "the peer presented no certificate").
			WithOp(opDial).
			WithCode(CodePeerNotPinned)
	}

	if wellknown.FingerprintOf(presented[0]) != pin {
		return errs.New(errs.KindUnavailable, "the peer is not the node this instance pinned").
			WithOp(opDial).
			WithCode(CodePeerNotPinned)
	}

	return nil
}
