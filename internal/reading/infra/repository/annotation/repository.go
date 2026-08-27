// Package annotation is the PostgreSQL adapter of the marks repository: it
// satisfies the port declared in internal/reading/domain/annotation and is the
// only place that knows reading.annotations exists.
package annotation

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/persist/readingdb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/shared/persist/revision"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate  = "reading/annotation: create"
	opUpdate  = "reading/annotation: update"
	opGetByID = "reading/annotation: get by id"
	opList    = "reading/annotation: list"
)

// Repository reads and writes what a reader has written, in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ annotation.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *readingdb.Queries {
	return readingdb.New(r.manager.Executor(ctx))
}

// Create records a mark.
func (r *Repository) Create(ctx context.Context, mark *annotation.Annotation) error {
	columns := revision.ToColumns(mark.Revision)

	err := r.queries(ctx).CreateAnnotation(ctx, readingdb.CreateAnnotationParams{
		ID:          mark.ID,
		EbookID:     mark.EbookID,
		Kind:        mark.Kind.String(),
		Text:        optionalString(mark.Text.String()),
		Locator:     mark.Locator.String(),
		VectorClock: columns.VectorClock,
		UpdatedAt:   columns.UpdatedAt,
		DeviceID:    columns.DeviceID,
		Deleted:     columns.Deleted,
	})

	return persist.Classify(err, opCreate)
}

// Update writes back the mark, the tombstone and the revision.
//
// The work is not in the statement. A mark is made in a work and stays in it,
// and a write that could move one would be a note about a passage that is not
// there.
func (r *Repository) Update(ctx context.Context, mark *annotation.Annotation) error {
	columns := revision.ToColumns(mark.Revision)

	rows, err := r.queries(ctx).UpdateAnnotation(ctx, readingdb.UpdateAnnotationParams{
		ID:          mark.ID,
		Kind:        mark.Kind.String(),
		Text:        optionalString(mark.Text.String()),
		Locator:     mark.Locator.String(),
		VectorClock: columns.VectorClock,
		UpdatedAt:   columns.UpdatedAt,
		DeviceID:    columns.DeviceID,
		Deleted:     columns.Deleted,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	// An UPDATE that matched nothing is not an error to PostgreSQL, and it is
	// exactly what a mark removed between the read and the write looks like.
	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByID reads a mark by primary key, tombstoned or not.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*annotation.Annotation, error) {
	row, err := r.queries(ctx).GetAnnotationByID(ctx, id)
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err, opGetByID)
		}

		return nil, persist.Classify(err, opGetByID)
	}

	return toDomain(&row), nil
}

// List reads one page of the marks in one work and the cursor the next page
// continues from.
//
// One more row than asked for is read, and the extra one is not returned. It
// is how the reply knows whether there is a next page without counting every
// mark in the book: a page that came back full might be the last one, and the
// only way to tell is to have looked one row past it.
func (r *Repository) List(
	ctx context.Context, query *annotation.Query,
) ([]*annotation.Annotation, annotation.Cursor, error) {
	rows, err := r.queries(ctx).ListAnnotations(ctx, readingdb.ListAnnotationsParams{
		EbookID:     query.EbookID,
		AfterCursor: !query.Cursor.IsZero(),
		CursorID:    query.Cursor.ID,
		PageSize:    int32(query.Size) + 1,
	})
	if err != nil {
		return nil, annotation.Cursor{}, persist.Classify(err, opList)
	}

	var next annotation.Cursor

	if len(rows) > query.Size {
		rows = rows[:query.Size]
		next = annotation.Cursor{ID: rows[len(rows)-1].ID}
	}

	marks := make([]*annotation.Annotation, 0, len(rows))

	for index := range rows {
		marks = append(marks, toDomain(&rows[index]))
	}

	return marks, next, nil
}

// notFound is the answer to a mark nobody has, one in somebody else's work and
// one that has been tombstoned. The caller decides which of the three it is
// looking at; the reply says the same thing for all of them.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no such annotation").
		WithOp(op).
		WithCode(annotation.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one the client holds.
//
// The value objects are cast rather than parsed. What is in the row was
// validated by the constructor that wrote it, and re-validating here would
// make a row this node can no longer parse — a kind added by a later version
// and replicated back, say — unreadable rather than merely unfamiliar.
func toDomain(row *readingdb.ReadingAnnotation) *annotation.Annotation {
	props := annotation.Props{
		EbookID: row.EbookID,
		Mark: annotation.Mark{
			Kind:    annotation.Kind(row.Kind),
			Locator: locator.Locator(row.Locator),
		},
		Revision: revision.FromColumns(row.VectorClock, row.UpdatedAt, row.DeviceID, row.Deleted),
	}

	// Absent means the reader wrote nothing, which the domain reads as the
	// zero value — a highlight or a bookmark they left uncommented.
	if row.Text != nil {
		props.Text = annotation.Text(*row.Text)
	}

	return annotation.Restore(row.ID, &props)
}

// optionalString renders an absent value as the NULL the column holds.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
