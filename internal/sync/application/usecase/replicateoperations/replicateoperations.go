// Package replicateoperations is UC09 between nodes: a peer offers this node a
// reader's changes, and this node stores and reconciles them (RF16, RN03).
//
// It is the ingest of a device's push with two questions asked first, and the
// two are what make it a different use case rather than a second controller
// over the same one. Which node is calling, because the certificate identifies
// a node and the log belongs to a reader. And whether that reader lets this
// node hold a copy — which is the only call in the whole contract refused on a
// reader's own instruction, and RN03 is why.
//
// What it does not check is RN10. A peer replicates many devices and is none of
// them, so there is no author for a batch to declare; what stands in its place
// is the authorization, and the ingest is told there is no device by being told
// nothing.
package replicateoperations

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/sync/application/service"
	command "github.com/anthonyvsmuller/quire/internal/sync/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
)

// ReplicateOperations accepts a peer's batch.
type ReplicateOperations struct {
	replicas service.Replicas
	ingest   command.Usecase[pushoperations.Input, pushoperations.Output]
}

// ReplicateOperations satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ReplicateOperations)(nil)

// New returns the use case over the catalogue and the ingest it delegates to.
func New(
	replicas service.Replicas,
	ingest command.Usecase[pushoperations.Input, pushoperations.Output],
) *ReplicateOperations {
	return &ReplicateOperations{replicas: replicas, ingest: ingest}
}

// Execute checks the caller and then stores what it offered.
//
// The order is the point. A peer that is not in the catalogue learns nothing
// about the reader it named, and a peer that is learns nothing about a reader
// who has not authorized it — including whether that reader exists here at all.
// Both refusals are the same words, because a peer able to tell them apart
// could enumerate this node's readers, which is not something an authorization
// for one of them should buy.
func (r *ReplicateOperations) Execute(ctx context.Context, input Input) (Output, error) {
	serverID, identified := r.replicas.Identify(ctx, input.Pin)
	if identified != nil {
		return Output{}, identified
	}

	if refused := r.replicas.Authorized(ctx, serverID, input.UserID); refused != nil {
		return Output{}, refused
	}

	// The author is left empty, which is how the ingest is told that the
	// caller is a node: there is no device for RN10 to check a batch against,
	// and what authorizes the call is the permission just verified.
	output, err := r.ingest.Execute(ctx, pushoperations.Input{
		UserID:     input.UserID,
		Operations: input.Operations,
	})
	if err != nil {
		return Output{}, err
	}

	return Output{Results: output.Results}, nil
}
