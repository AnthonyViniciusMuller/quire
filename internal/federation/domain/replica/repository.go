package replica

import (
	"context"
	"uuid"
)

// The stable machine-readable codes a repository reports.
const (
	// CodeNotFound is no such authorization, and also the answer to one that
	// belongs to another reader. The two are one code because they are one
	// fact to whoever asked: that node holds nothing of theirs.
	CodeNotFound = "replica_authorization_not_found"
	// CodePairKnown is a second authorization for a pair that already has one.
	// It is what the unique constraint reports, and the caller answers it by
	// granting the row that is already there.
	CodePairKnown = "replica_authorization_exists"
)

// Repository is the port through which the use cases of the federation slice
// read and write replica authorizations. It belongs to the domain; what
// satisfies it lives in internal/federation/infra/repository/replica.
type Repository interface {
	// Create grants a permission that did not exist, and reports
	// errs.KindAlreadyExists with CodePairKnown when the pair already has a
	// row.
	Create(ctx context.Context, authorization *Replica) error

	// Update writes back the three columns a decision changes.
	Update(ctx context.Context, authorization *Replica) error

	// GetByPair reads the authorization of one reader for one node, active or
	// not. Reading a revoked one is the point: re-authorizing a node the
	// reader once revoked writes that row rather than a second one.
	GetByPair(ctx context.Context, userID, serverID uuid.UUID) (*Replica, error)

	// ListByUser reads a reader's authorizations, newest decision first.
	// Revoked ones are hidden unless asked for, and they are what explains a
	// peer that still holds data.
	//
	// It returns the authorizations and not the nodes they name. The reply the
	// reader is shown carries a domain beside each one, and the use case
	// assembles that from the catalogue it has already read — a reader's
	// federation is the handful of nodes they chose, which is the same reason
	// neither list is paginated.
	ListByUser(ctx context.Context, userID uuid.UUID, includeInactive bool) ([]*Replica, error)

	// CountActiveForServer is how many readers still allow the node to hold a
	// copy, across the whole instance.
	//
	// It counts every reader and not just the caller, because the catalogue is
	// node-wide: forgetting a node another reader still replicates to would
	// leave that reader unable to revoke it, and RN03 is the promise that they
	// can.
	CountActiveForServer(ctx context.Context, serverID uuid.UUID) (int64, error)
}
