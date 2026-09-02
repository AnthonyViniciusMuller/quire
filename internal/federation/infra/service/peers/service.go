// Package peers is the client this node is of other nodes on a reader's
// behalf: the two calls of C22, over the connection the shared dialer keeps.
//
// What it adds to the connection is the translation and the bound. The
// dial — the pin over the peer's public key, the certificate this node
// presents in turn, one connection kept per node — is [grpcx.PeerDialer],
// which the sync slice offers changes over as well; what this package knows
// is which node a catalogue row names and how an admission is spelled on the
// wire.
package peers

import (
	"context"
	"time"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opAdmit    = "federation/peers: admit"
	opWithdraw = "federation/peers: withdraw"
)

// The stable machine-readable codes this adapter raises.
const (
	// CodeUnreachablePeer is a node that did not answer the call.
	CodeUnreachablePeer = "peer_unreachable"
	// CodeUnaddressablePeer is a node the catalogue holds no way of dialing.
	CodeUnaddressablePeer = "peer_not_addressable"
	// CodePeerRefused is a node that answered, and refused.
	CodePeerRefused = "peer_refused"
)

// Service makes the calls over the shared dialer.
type Service struct {
	catalogue federationserver.Repository
	dialer    *grpcx.PeerDialer
	timeout   time.Duration
}

// Service satisfies the port the use cases hold.
var _ service.Peers = (*Service)(nil)

// New returns the adapter over the catalogue that names the peers, the dialer
// that reaches them, and the bound one call may take.
//
// The bound is the one a discovery lookup has, and for the same reason: a
// peer belongs to another operator, and a call to it is made from inside a
// unit of work of this node's that should not wait on somebody else's
// outage.
func New(dialer *grpcx.PeerDialer, catalogue federationserver.Repository, timeout time.Duration) *Service {
	return &Service{catalogue: catalogue, dialer: dialer, timeout: timeout}
}

// Admit tells the node that the reader has authorized it.
func (s *Service) Admit(ctx context.Context, serverID uuid.UUID, admission *service.Admission) error {
	client, err := s.client(ctx, serverID, opAdmit)
	if err != nil {
		return err
	}

	devices := make([]*quirev1.ReplicatedDevice, 0, len(admission.Devices))
	for _, appliance := range admission.Devices {
		devices = append(devices, &quirev1.ReplicatedDevice{
			Id:       appliance.ID.String(),
			Name:     appliance.Name,
			Platform: appliance.Platform,
		})
	}

	bounded, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err = client.AdmitReplica(bounded, &quirev1.AdmitReplicaRequest{
		Reader: &quirev1.ReplicatedReader{
			Id:          admission.Reader.ID.String(),
			LocalName:   admission.Reader.LocalName,
			DisplayName: admission.Reader.DisplayName,
		},
		Devices:         devices,
		ReplicatesFiles: admission.ReplicatesFiles,
	})

	return answered(err, opAdmit)
}

// Withdraw tells the node that the reader has withdrawn the permission.
func (s *Service) Withdraw(ctx context.Context, serverID, userID uuid.UUID) error {
	client, err := s.client(ctx, serverID, opWithdraw)
	if err != nil {
		return err
	}

	bounded, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err = client.WithdrawReplica(bounded, &quirev1.WithdrawReplicaRequest{UserId: userID.String()})

	return answered(err, opWithdraw)
}

// client returns the stub for one node, over the connection the dialer keeps
// for it. The catalogue is read on every call, because it is where a changed
// pin or a moved authority shows up.
func (s *Service) client(ctx context.Context, serverID uuid.UUID, op string) (quirev1.FederationServiceClient, error) {
	peer, err := s.catalogue.GetByID(ctx, serverID)
	if err != nil {
		return nil, err
	}

	if peer.GRPCAuthority.IsZero() {
		return nil, errs.New(errs.KindFailedPrecondition, "the peer publishes no gRPC authority").
			WithOp(op).
			WithCode(CodeUnaddressablePeer)
	}

	channel, err := s.dialer.Dial(serverID, peer.GRPCAuthority.String(), peer.CertificateFingerprint.String())
	if err != nil {
		return nil, err
	}

	return quirev1.NewFederationServiceClient(channel), nil
}

// answered classifies what the peer said, or did not.
//
// A peer that did not answer — no route, a handshake the pin refused, the
// bound reached — is Unavailable, and the caller decides whether to try
// again. A peer that answered with a refusal has read the call and will read
// it the same way again, and its own message is carried across: it is the
// one thing an operator on this side can act on, and a node says nothing in a
// status that it would not say to a device.
func answered(err error, op string) error {
	if err == nil {
		return nil
	}

	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Unknown, codes.Internal:
		return errs.Wrap(err, errs.KindUnavailable, "the peer did not answer").
			WithOp(op).
			WithCode(CodeUnreachablePeer)
	default:
		return errs.Wrap(err, errs.KindFailedPrecondition, "the peer refused: "+status.Convert(err).Message()).
			WithOp(op).
			WithCode(CodePeerRefused)
	}
}
