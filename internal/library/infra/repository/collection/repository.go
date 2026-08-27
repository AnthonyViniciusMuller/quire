// Package collection is the PostgreSQL adapter of the groupings repository: it
// satisfies the port declared in internal/library/domain/collection and is the
// only place that knows library.collections exists.
package collection

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/persist/revision"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate           = "library/collection: create"
	opUpdate           = "library/collection: update"
	opGetByID          = "library/collection: get by id"
	opGetByIDForUpdate = "library/collection: get by id for update"
	opList             = "library/collection: list"
)

// Repository reads and writes a reader's groupings in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ collection.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *librarydb.Queries {
	return librarydb.New(r.manager.Executor(ctx))
}

// Create records a grouping.
func (r *Repository) Create(ctx context.Context, grouping *collection.Collection) error {
	columns := revision.ToColumns(grouping.Revision)

	err := r.queries(ctx).CreateCollection(ctx, librarydb.CreateCollectionParams{
		ID:          grouping.ID,
		UserID:      grouping.UserID,
		Name:        grouping.Name.String(),
		Kind:        grouping.Kind.String(),
		Description: optionalString(grouping.Description.String()),
		CreatedAt:   grouping.CreatedAt,
		VectorClock: columns.VectorClock,
		UpdatedAt:   columns.UpdatedAt,
		DeviceID:    columns.DeviceID,
		Deleted:     columns.Deleted,
	})

	return persist.Classify(err, opCreate)
}

// Update writes back the description, the tombstone and the revision.
func (r *Repository) Update(ctx context.Context, grouping *collection.Collection) error {
	columns := revision.ToColumns(grouping.Revision)

	rows, err := r.queries(ctx).UpdateCollection(ctx, librarydb.UpdateCollectionParams{
		ID:          grouping.ID,
		Name:        grouping.Name.String(),
		Kind:        grouping.Kind.String(),
		Description: optionalString(grouping.Description.String()),
		VectorClock: columns.VectorClock,
		UpdatedAt:   columns.UpdatedAt,
		DeviceID:    columns.DeviceID,
		Deleted:     columns.Deleted,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByID reads a grouping by primary key, tombstoned or not.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*collection.Collection, error) {
	row, err := r.queries(ctx).GetCollectionByID(ctx, id)
	if err != nil {
		return nil, readError(err, opGetByID)
	}

	return toDomain(&row), nil
}

// GetByIDForUpdate is GetByID holding the row until the transaction ends.
func (r *Repository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*collection.Collection, error) {
	row, err := r.queries(ctx).GetCollectionByIDForUpdate(ctx, id)
	if err != nil {
		return nil, readError(err, opGetByIDForUpdate)
	}

	return toDomain(&row), nil
}

// List reads a reader's groupings, narrowed to one work's when ebookID says so.
func (r *Repository) List(ctx context.Context, userID, ebookID uuid.UUID) ([]*collection.Collection, error) {
	rows, err := r.queries(ctx).ListCollections(ctx, librarydb.ListCollectionsParams{
		UserID:       userID,
		HoldingEbook: ebookID != uuid.UUID{},
		EbookID:      ebookID,
	})
	if err != nil {
		return nil, persist.Classify(err, opList)
	}

	groupings := make([]*collection.Collection, 0, len(rows))
	for index := range rows {
		groupings = append(groupings, toDomain(&rows[index]))
	}

	return groupings, nil
}

// readError is the classification both single-row reads share.
func readError(err error, op string) error {
	if persist.IsNoRows(err) {
		return notFound(err, op)
	}

	return persist.Classify(err, op)
}

// notFound is the answer to a grouping nobody has, one that is somebody
// else's, and one that has been tombstoned.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no such grouping").
		WithOp(op).
		WithCode(collection.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one every filing references.
func toDomain(row *librarydb.LibraryCollection) *collection.Collection {
	props := collection.Props{
		UserID: row.UserID,
		Details: collection.Details{
			Name: collection.Name(row.Name),
			Kind: collection.Kind(row.Kind),
		},
		CreatedAt: row.CreatedAt,
		Revision:  revision.FromColumns(row.VectorClock, row.UpdatedAt, row.DeviceID, row.Deleted),
	}

	if row.Description != nil {
		props.Description = collection.Description(*row.Description)
	}

	return collection.Restore(row.ID, &props)
}

// optionalString renders an absent value as the NULL the column holds.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
