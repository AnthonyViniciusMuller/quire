// Package peers is the outbound half of node-to-node replication: the gRPC
// client this node offers a reader's changes to another node with.
//
// It is the only place in the node where Quire is a client of Quire, and the
// only one that has to establish trust with a party it shares no root of trust
// with. Two nodes belong to two operators, so there is no certificate authority
// both of them answer to; what stands in for one is the pin each publishes in
// its own discovery document and the other stores in its catalogue (RNF08).
//
// # What the pin is over, and what that costs
//
// The digest covers the SubjectPublicKeyInfo and not the certificate, which is
// C12 in docs/tcc-corrections.md: a digest of the certificate stops matching at
// the first ACME renewal, so pinning it would break replication between every
// pair of nodes about every sixty days and would train an operator to clear the
// one alarm a substituted certificate would raise.
//
// Checking it means turning off the verification the library would do and doing
// another. That is not a weakening: the chain a public CA would sign is not
// evidence about *this* peer, since any certificate that CA issues chains
// equally well. The pin is evidence about this peer, and it is the only thing
// here that is.
//
// # When there is no pin
//
// A peer that published none is not replicated to outside development. The
// alternative is to dial it without checking anything, which is how a pin
// becomes optional in practice: a node that talks to unpinned peers is a node
// whose pins protect nothing, because an attacker only has to serve a document
// without one. The development profile is the exception, and it says so at
// startup rather than in a comment.
package peers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"sync"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/convert"
)

// opReplicate is the operation reported by this package, in the form the errs
// package expects.
const opReplicate = "sync/peers: replicate"

// The stable machine-readable codes this adapter raises.
const (
	// CodeUnreachablePeer is a node this instance could not talk to.
	CodeUnreachablePeer = "peer_unreachable"
	// CodeUnaddressablePeer is a node the catalogue holds and cannot dial: it
	// published no gRPC authority, or no pin on a node that requires one.
	CodeUnaddressablePeer = "peer_not_addressable"
	// CodeWrongPeer is a node whose certificate is not the one the catalogue
	// pinned, which is the alarm the pin exists to raise.
	CodeWrongPeer = "peer_certificate_not_pinned"
)

// Service offers changes to other nodes.
type Service struct {
	catalogue federationserver.Repository
	identity  *tls.Certificate
	insecure  bool

	mu          sync.Mutex
	connections map[uuid.UUID]*connection
}

// Service satisfies the port the use cases hold.
var _ service.Peers = (*Service)(nil)

// connection is one open channel to a peer, and what it was opened against.
//
// The two are kept together because a peer that has been rediscovered may be
// answering somewhere else or presenting a different key, and a cached
// connection to where it used to be is worse than no cache at all.
type connection struct {
	client    quirev1.SyncServiceClient
	channel   *grpc.ClientConn
	authority federationserver.GRPCAuthority
	pin       federationserver.Fingerprint
}

// New returns the client over the catalogue and the node's own credentials.
//
// The certificate is this node's, and it is presented rather than merely held:
// the exchange is mutual, because the destination has to know which node is
// offering it a reader's changes before it can check that reader's
// authorization. A node with none can still replicate under
// QUIRE_FEDERATION_ALLOW_INSECURE_DISCOVERY, which is the same switch the
// discovery client uses and is refused in production by the configuration
// itself.
func New(cfg *config.Federation, catalogue federationserver.Repository) (*Service, error) {
	client := &Service{
		catalogue:   catalogue,
		insecure:    cfg.AllowInsecureDiscovery,
		connections: map[uuid.UUID]*connection{},
	}

	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, errs.Wrap(err, errs.KindFailedPrecondition,
				"the node certificate could not be loaded").WithOp(opReplicate)
		}

		client.identity = &certificate
	}

	return client, nil
}

// Close releases every channel this node holds open.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, held := range s.connections {
		_ = held.channel.Close()

		delete(s.connections, id)
	}

	return nil
}

// Replicate offers a reader's changes to one node.
func (s *Service) Replicate(
	ctx context.Context,
	serverID, userID uuid.UUID,
	operations []*operation.Operation,
) ([]operation.Result, error) {
	client, err := s.client(ctx, serverID)
	if err != nil {
		return nil, err
	}

	reply, err := client.ReplicateOperations(ctx, &quirev1.ReplicateOperationsRequest{
		UserId:     userID.String(),
		Operations: convert.Operations(operations),
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "the peer did not answer").
			WithOp(opReplicate).
			WithCode(CodeUnreachablePeer)
	}

	return results(reply.GetResults()), nil
}

// client returns the channel to a peer, opening one when there is none and
// replacing one that was opened against an address or a key the catalogue no
// longer holds.
func (s *Service) client(
	ctx context.Context, serverID uuid.UUID,
) (quirev1.SyncServiceClient, error) {
	peer, err := s.catalogue.GetByID(ctx, serverID)
	if err != nil {
		return nil, err
	}

	if peer.GRPCAuthority.IsZero() {
		return nil, s.unaddressable("the peer publishes no gRPC authority")
	}

	if peer.CertificateFingerprint.IsZero() && !s.insecure {
		return nil, s.unaddressable("the peer publishes no certificate pin")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if held, open := s.connections[serverID]; open {
		if held.authority == peer.GRPCAuthority && held.pin == peer.CertificateFingerprint {
			return held.client, nil
		}

		_ = held.channel.Close()
		delete(s.connections, serverID)
	}

	channel, err := grpc.NewClient(peer.GRPCAuthority.String(), grpc.WithTransportCredentials(
		s.credentials(peer.CertificateFingerprint)))
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "the peer could not be dialed").
			WithOp(opReplicate).
			WithCode(CodeUnreachablePeer)
	}

	opened := &connection{
		client:    quirev1.NewSyncServiceClient(channel),
		channel:   channel,
		authority: peer.GRPCAuthority,
		pin:       peer.CertificateFingerprint,
	}
	s.connections[serverID] = opened

	return opened.client, nil
}

// credentials are what this node presents to a peer and what it checks the
// peer against.
func (s *Service) credentials(pin federationserver.Fingerprint) credentials.TransportCredentials {
	if pin.IsZero() {
		// Only reachable under the development switch, which the configuration
		// refuses to set in production.
		return insecure.NewCredentials()
	}

	settings := &tls.Config{
		// The library's own verification is turned off because it is answering
		// the wrong question: a chain signed by a public authority says the
		// certificate is well formed, not that it belongs to this peer, and
		// two nodes run by two operators share no authority in any case. What
		// replaces it is below, and it is stricter — it admits exactly one key.
		InsecureSkipVerify: true, //nolint:gosec // replaced by the pin check below, which is narrower.
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyPin(raw, pin)
		},
		// The same check again, on the handshake rather than on the
		// certificates, because a resumed session presents none: TLS 1.3
		// resumption skips VerifyPeerCertificate entirely, so a connection
		// resumed against a peer that had since changed its key would never be
		// checked. This one runs on every handshake, resumed or full.
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyPresented(state.PeerCertificates, pin)
		},
	}

	if s.identity != nil {
		settings.Certificates = []tls.Certificate{*s.identity}
	}

	return credentials.NewTLS(settings)
}

// verifyPin checks the leaf of a full handshake against what the catalogue
// holds.
//
// The leaf is the first certificate, as it is in every TLS handshake and as
// wellknown.Fingerprint assumes when it reads a bundle. Nothing behind it is
// looked at: the chain is what a certificate authority vouches for, and the pin
// is what the peer itself published.
func verifyPin(raw [][]byte, pin federationserver.Fingerprint) error {
	if len(raw) == 0 {
		return errs.New(errs.KindUnavailable, "the peer presented no certificate").
			WithOp(opReplicate).
			WithCode(CodeWrongPeer)
	}

	leaf, err := x509.ParseCertificate(raw[0])
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "the peer presented a certificate this node cannot read").
			WithOp(opReplicate).
			WithCode(CodeWrongPeer)
	}

	return verifyPresented([]*x509.Certificate{leaf}, pin)
}

// verifyPresented checks the leaf of an established connection against what the
// catalogue holds.
//
// The leaf is the first certificate, as it is in every TLS handshake and as
// wellknown.Fingerprint assumes when it reads a bundle. Nothing behind it is
// looked at: the chain is what a certificate authority vouches for, and the pin
// is what the peer itself published.
func verifyPresented(presented []*x509.Certificate, pin federationserver.Fingerprint) error {
	if len(presented) == 0 {
		return errs.New(errs.KindUnavailable, "the peer presented no certificate").
			WithOp(opReplicate).
			WithCode(CodeWrongPeer)
	}

	if wellknown.FingerprintOf(presented[0]) != pin.String() {
		return errs.New(errs.KindUnavailable, "the peer is not the node this instance pinned").
			WithOp(opReplicate).
			WithCode(CodeWrongPeer)
	}

	return nil
}

// unaddressable is the answer for a peer the catalogue holds and this node
// cannot dial.
//
// It is an error and not a silence, because the deliveries stay owed: a peer
// that publishes no authority is a row an operator has to look at, and one that
// disappears from the log is a row nobody ever will.
func (s *Service) unaddressable(reason string) error {
	return errs.New(errs.KindFailedPrecondition, reason).
		WithOp(opReplicate).
		WithCode(CodeUnaddressablePeer)
}

// results reads the verdicts a destination answered with.
//
// A verdict this node cannot read is dropped rather than guessed at: the
// delivery it belonged to then stays owed and is offered again, which is safe
// because receiving is idempotent by the identifier the author minted.
func results(answered []*quirev1.OperationResult) []operation.Result {
	read := make([]operation.Result, 0, len(answered))

	for _, result := range answered {
		id, err := uuid.Parse(result.GetOperationId())
		if err != nil {
			continue
		}

		read = append(read, operation.Result{
			OperationID: id,
			Verdict: operation.Verdict{
				Outcome: outcome(result.GetOutcome()),
				Detail:  result.GetDetail(),
			},
		})
	}

	return read
}

// outcome reads a verdict.
func outcome(answered quirev1.OperationOutcome) operation.Outcome {
	switch answered {
	case quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED:
		return operation.OutcomeApplied
	case quirev1.OperationOutcome_OPERATION_OUTCOME_DUPLICATE:
		return operation.OutcomeDuplicate
	case quirev1.OperationOutcome_OPERATION_OUTCOME_SUPERSEDED:
		return operation.OutcomeSuperseded
	case quirev1.OperationOutcome_OPERATION_OUTCOME_REJECTED:
		return operation.OutcomeRejected
	case quirev1.OperationOutcome_OPERATION_OUTCOME_UNSPECIFIED:
		return operation.OutcomeUnspecified
	default:
		return operation.OutcomeUnspecified
	}
}
