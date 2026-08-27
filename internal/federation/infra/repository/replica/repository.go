// Package replica is the PostgreSQL adapter of the replica authorization
// repository: it satisfies the port declared in
// internal/federation/domain/replica and is the only place that knows
// federation.user_replicas exists.
package replica

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/persist/federationdb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate               = "federation/replica: create"
	opUpdate               = "federation/replica: update"
	opGetByPair            = "federation/replica: get by pair"
	opListByUser           = "federation/replica: list by user"
	opCountActiveForServer = "federation/replica: count active for server"
)

// constraintPair is the name of the unique constraint on the pair, as it
// appears in the driver error. It is what a second authorization for a node
// the reader has already decided about breaks.
const constraintPair = "user_replicas_pair_key"

// Repository reads and writes replica authorizations in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ replica.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *federationdb.Queries {
	return federationdb.New(r.manager.Executor(ctx))
}

// Create grants a permission that did not exist.
func (r *Repository) Create(ctx context.Context, authorization *replica.Replica) error {
	err := r.queries(ctx).CreateReplicaAuthorization(ctx, federationdb.CreateReplicaAuthorizationParams{
		ID:              authorization.ID,
		UserID:          authorization.UserID,
		ServerID:        authorization.ServerID,
		AuthorizedAt:    authorization.AuthorizedAt,
		ReplicatesFiles: authorization.ReplicatesFiles,
		Active:          authorization.Active,
	})

	if persist.IsUniqueViolation(err, constraintPair) {
		return errs.Wrap(err, errs.KindAlreadyExists, "that node is already authorized for this reader").
			WithOp(opCreate).
			WithCode(replica.CodePairKnown).
			WithField("server_id", "one row holds the whole history of this decision, and it exists")
	}

	return persist.Classify(err, opCreate)
}

// Update writes back the three columns a decision changes.
func (r *Repository) Update(ctx context.Context, authorization *replica.Replica) error {
	rows, err := r.queries(ctx).UpdateReplicaAuthorization(ctx, federationdb.UpdateReplicaAuthorizationParams{
		ID:              authorization.ID,
		AuthorizedAt:    authorization.AuthorizedAt,
		ReplicatesFiles: authorization.ReplicatesFiles,
		Active:          authorization.Active,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByPair reads the authorization of one reader for one node.
func (r *Repository) GetByPair(ctx context.Context, userID, serverID uuid.UUID) (*replica.Replica, error) {
	row, err := r.queries(ctx).GetReplicaAuthorizationByPair(ctx,
		federationdb.GetReplicaAuthorizationByPairParams{UserID: userID, ServerID: serverID})
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err, opGetByPair)
		}

		return nil, persist.Classify(err, opGetByPair)
	}

	return toDomain(&row), nil
}

// ListByUser reads a reader's authorizations.
func (r *Repository) ListByUser(
	ctx context.Context,
	userID uuid.UUID,
	includeInactive bool,
) ([]*replica.Replica, error) {
	rows, err := r.queries(ctx).ListReplicaAuthorizationsByUser(ctx,
		federationdb.ListReplicaAuthorizationsByUserParams{UserID: userID, IncludeInactive: includeInactive})
	if err != nil {
		return nil, persist.Classify(err, opListByUser)
	}

	authorizations := make([]*replica.Replica, 0, len(rows))
	for index := range rows {
		authorizations = append(authorizations, toDomain(&rows[index]))
	}

	return authorizations, nil
}

// CountActiveForServer is how many readers still allow the node to hold a copy.
func (r *Repository) CountActiveForServer(ctx context.Context, serverID uuid.UUID) (int64, error) {
	count, err := r.queries(ctx).CountActiveReplicaAuthorizationsForServer(ctx, serverID)
	if err != nil {
		return 0, persist.Classify(err, opCountActiveForServer)
	}

	return count, nil
}

// notFound is the answer to an authorization that is not here, and to one that
// belongs to another reader.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "that node holds nothing of this reader's").
		WithOp(op).
		WithCode(replica.CodeNotFound)
}

// toDomain rebuilds the entity from the row.
func toDomain(row *federationdb.FederationUserReplica) *replica.Replica {
	return replica.Restore(row.ID, &replica.Props{
		UserID:          row.UserID,
		ServerID:        row.ServerID,
		AuthorizedAt:    row.AuthorizedAt,
		ReplicatesFiles: row.ReplicatesFiles,
		Active:          row.Active,
	})
}
