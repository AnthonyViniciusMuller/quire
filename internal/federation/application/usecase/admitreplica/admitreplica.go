// Package admitreplica is the destination's half of UC15 (RF16, RN03): a peer
// telling this node that a reader has authorized it, and this node recording
// what it has to hold before it can accept anything of theirs.
//
// It exists because of C22 in docs/tcc-corrections.md. The permission of UC15
// is recorded on the origin, and the destination checks one of its own before
// it accepts a single operation; nothing in the specification carried it from
// one to the other, so a federation assembled through the API could not
// replicate at all.
//
// The caller is identified by its certificate and not by anything it claims.
// The pin is looked up in this node's own catalogue, and a node nobody here
// has added is refused: the alternative, recording whatever origin the call
// names, would make this a node anybody could fill with readers who never
// asked for it — which is exactly the promise RN03 makes to the reader.
package admitreplica

import (
	"context"
	"errors"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opExecute = "federation/admitreplica: execute"
	opCaller  = "federation/admitreplica: caller"
)

// CodeUnknownPeer is a certificate no node in the catalogue published, or one
// the operator has stopped.
const CodeUnknownPeer = "peer_not_known"

// AdmitReplica records a reader a peer replicates here.
type AdmitReplica struct {
	servers     server.Repository
	replicas    replica.Repository
	readers     service.Readers
	clock       service.Clock
	transaction service.Transaction
}

// AdmitReplica satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*AdmitReplica)(nil)

// New returns the use case over its dependencies.
func New(
	servers server.Repository,
	replicas replica.Repository,
	readers service.Readers,
	clock service.Clock,
	transaction service.Transaction,
) *AdmitReplica {
	return &AdmitReplica{
		servers:     servers,
		replicas:    replicas,
		readers:     readers,
		clock:       clock,
		transaction: transaction,
	}
}

// Execute records the reader, their devices and the permission, in one unit of
// work.
//
// A reader admitted twice is admitted once: the reader and the devices this
// node already holds are left as they are, the devices it does not are added,
// and the permission is granted again as the origin now states it. That is
// what lets the origin call this at every binding rather than once — a replica
// that missed a device is caught up by the next call, and there is nothing to
// undo when the call is repeated.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (a *AdmitReplica) Execute(ctx context.Context, input Input) (Output, error) {
	origin, err := Caller(ctx, a.servers, input.Pin)
	if err != nil {
		return Output{}, err
	}

	err = a.transaction.Within(ctx, func(ctx context.Context) error {
		if admitErr := a.readers.Admit(ctx, origin.ID, &input.Reader, input.Devices); admitErr != nil {
			return admitErr
		}

		return a.grant(ctx, &input, origin)
	})
	if err != nil {
		return Output{}, err
	}

	return Output{}, nil
}

// grant records that the origin may send this reader's changes, reusing the
// row the pair already has.
func (a *AdmitReplica) grant(ctx context.Context, input *Input, origin *server.Server) error {
	existing, err := a.replicas.GetByPair(ctx, input.Reader.ID, origin.ID)

	switch {
	case err == nil:
		existing.Grant(input.ReplicatesFiles, a.clock.Now())

		return a.replicas.Update(ctx, existing)
	case errors.Is(err, errs.KindNotFound):
		granted, newErr := replica.New(input.Reader.ID, origin.ID, input.ReplicatesFiles, a.clock.Now())
		if newErr != nil {
			return newErr
		}

		return a.replicas.Create(ctx, granted)
	default:
		return err
	}
}

// Caller returns the node the pin names, or refuses the call.
//
// It is exported because the use case that withdraws a permission identifies
// its caller the same way and has to refuse the same callers: a certificate
// the catalogue does not name, and a node the operator has stopped. The
// refusal says nothing about which of the two it was — a peer that could tell
// them apart would know whether it had ever been in this node's catalogue.
func Caller(ctx context.Context, servers server.Repository, pin string) (*server.Server, error) {
	if pin == "" {
		return nil, unknown()
	}

	node, err := servers.GetByFingerprint(ctx, server.Fingerprint(pin))
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			return nil, unknown()
		}

		return nil, err
	}

	if !node.Active {
		return nil, unknown()
	}

	return node, nil
}

// unknown is the answer to a certificate no active node in the catalogue
// published.
func unknown() error {
	return errs.New(errs.KindPermissionDenied, "this node is not federating with you").
		WithOp(opCaller).
		WithCode(CodeUnknownPeer)
}
