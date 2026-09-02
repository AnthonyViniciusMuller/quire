// Package peers is the outbound half of node-to-node replication: the gRPC
// client this node offers a reader's changes to another node with.
//
// What it adds to the connection is the translation and nothing else. How a
// peer is dialed — the pin over its public key, the certificate this node
// presents in turn, the one connection kept per node — is the shared
// [grpcx.PeerDialer], because the federation slice makes calls to the same
// peers over the same connections; what this package knows is which node a
// catalogue row names, and how a batch of operations is spelled on the wire.
package peers

import (
	"context"
	"uuid"

	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/convert"
)

// opReplicate is the operation reported by this package, in the form the errs
// package expects.
const opReplicate = "sync/peers: replicate"

// The stable machine-readable codes this adapter raises.
const (
	// CodeUnreachablePeer is a node that did not answer the call.
	CodeUnreachablePeer = "peer_unreachable"
	// CodeUnaddressablePeer is a node the catalogue holds no way of dialing:
	// no gRPC authority, or no pin outside development.
	CodeUnaddressablePeer = "peer_not_addressable"
)

// Service offers operations to peers over the shared dialer.
type Service struct {
	catalogue federationserver.Repository
	dialer    *grpcx.PeerDialer
}

// Service satisfies the port the use cases hold.
var _ service.Peers = (*Service)(nil)

// New returns the adapter over the catalogue that names the peers and the
// dialer that reaches them.
func New(dialer *grpcx.PeerDialer, catalogue federationserver.Repository) *Service {
	return &Service{catalogue: catalogue, dialer: dialer}
}

// Replicate offers one reader's operations to one node and returns what the
// node did with each.
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

// client returns the stub for one node, over the connection the dialer keeps
// for it.
//
// The catalogue is read on every call rather than once, because it is where a
// changed pin or a moved authority shows up: a node that re-read its peer's
// document has a new row, and the dialer replaces the connection when what
// the row says has changed.
func (s *Service) client(
	ctx context.Context, serverID uuid.UUID,
) (quirev1.SyncServiceClient, error) {
	peer, err := s.catalogue.GetByID(ctx, serverID)
	if err != nil {
		return nil, err
	}

	if peer.GRPCAuthority.IsZero() {
		return nil, errs.New(errs.KindFailedPrecondition, "the peer publishes no gRPC authority").
			WithOp(opReplicate).
			WithCode(CodeUnaddressablePeer)
	}

	channel, err := s.dialer.Dial(serverID, peer.GRPCAuthority.String(), peer.CertificateFingerprint.String())
	if err != nil {
		return nil, err
	}

	return quirev1.NewSyncServiceClient(channel), nil
}

// results reads the peer's verdicts, dropping any it cannot address.
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

// outcome reads a verdict off the wire.
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
