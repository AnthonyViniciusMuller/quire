// Package ebook is the PostgreSQL adapter of the works repository: it
// satisfies the port declared in internal/library/domain/ebook and is the only
// place that knows library.ebooks exists.
package ebook

import (
	"context"
	"encoding/json"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/library/infra/repository/revision"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate       = "library/ebook: create"
	opUpdate       = "library/ebook: update"
	opGetByID      = "library/ebook: get by id"
	opHoldsContent = "library/ebook: holds content"
	opList         = "library/ebook: list"
)

// Repository reads and writes a reader's works in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ ebook.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *librarydb.Queries {
	return librarydb.New(r.manager.Executor(ctx))
}

// Create records a work.
func (r *Repository) Create(ctx context.Context, work *ebook.Ebook) error {
	extra, err := marshalMetadata(work.Extra, opCreate)
	if err != nil {
		return err
	}

	columns := revision.ToColumns(work.Revision)

	err = r.queries(ctx).CreateEbook(ctx, librarydb.CreateEbookParams{
		ID:            work.ID,
		UserID:        work.UserID,
		Title:         work.Title.String(),
		Author:        optionalString(work.Author.String()),
		Publisher:     optionalString(work.Publisher.String()),
		Language:      optionalString(work.Language.String()),
		Format:        work.Format.String(),
		ContentHash:   work.Hash.String(),
		SizeBytes:     optionalInt64(work.Size.Int64()),
		ExtraMetadata: extra,
		ImportedAt:    work.ImportedAt,
		VectorClock:   columns.VectorClock,
		UpdatedAt:     columns.UpdatedAt,
		DeviceID:      columns.DeviceID,
		Deleted:       columns.Deleted,
	})

	return persist.Classify(err, opCreate)
}

// Update writes back the description, the tombstone and the revision.
//
// The file is not in the statement. The format, the digest and the length are
// fixed at import, and a statement that could change them would let a row
// describe a file it is not.
func (r *Repository) Update(ctx context.Context, work *ebook.Ebook) error {
	extra, err := marshalMetadata(work.Extra, opUpdate)
	if err != nil {
		return err
	}

	columns := revision.ToColumns(work.Revision)

	rows, err := r.queries(ctx).UpdateEbook(ctx, librarydb.UpdateEbookParams{
		ID:            work.ID,
		Title:         work.Title.String(),
		Author:        optionalString(work.Author.String()),
		Publisher:     optionalString(work.Publisher.String()),
		Language:      optionalString(work.Language.String()),
		ExtraMetadata: extra,
		VectorClock:   columns.VectorClock,
		UpdatedAt:     columns.UpdatedAt,
		DeviceID:      columns.DeviceID,
		Deleted:       columns.Deleted,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	// An UPDATE that matched nothing is not an error to PostgreSQL, and it is
	// exactly what a work removed between the read and the write looks like.
	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByID reads a work by primary key, tombstoned or not.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*ebook.Ebook, error) {
	row, err := r.queries(ctx).GetEbookByID(ctx, id)
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err, opGetByID)
		}

		return nil, persist.Classify(err, opGetByID)
	}

	return toDomain(&row)
}

// HoldsContent reports whether the reader has any work naming the digest.
func (r *Repository) HoldsContent(
	ctx context.Context, userID uuid.UUID, hash ebook.ContentHash,
) (bool, error) {
	holds, err := r.queries(ctx).UserHoldsContent(ctx, librarydb.UserHoldsContentParams{
		UserID:      userID,
		ContentHash: hash.String(),
	})
	if err != nil {
		return false, persist.Classify(err, opHoldsContent)
	}

	return holds, nil
}

// List reads one page of a reader's collection and the cursor the next page
// continues from.
//
// One more row than asked for is read, and the extra one is not returned. It
// is how the reply knows whether there is a next page without counting the
// whole collection: a page that came back full might be the last one, and the
// only way to tell is to have looked one row past it.
func (r *Repository) List(
	ctx context.Context, query *ebook.Query,
) ([]*ebook.Ebook, ebook.Cursor, error) {
	rows, err := r.queries(ctx).ListEbooks(ctx, librarydb.ListEbooksParams{
		UserID:           query.UserID,
		InCollection:     query.CollectionID != uuid.UUID{},
		CollectionID:     query.CollectionID,
		AfterCursor:      !query.Cursor.IsZero(),
		CursorImportedAt: query.Cursor.ImportedAt,
		CursorID:         query.Cursor.ID,
		PageSize:         int32(query.Size) + 1,
	})
	if err != nil {
		return nil, ebook.Cursor{}, persist.Classify(err, opList)
	}

	var next ebook.Cursor

	if len(rows) > query.Size {
		rows = rows[:query.Size]
		last := rows[len(rows)-1]
		next = ebook.Cursor{ImportedAt: last.ImportedAt, ID: last.ID}
	}

	works := make([]*ebook.Ebook, 0, len(rows))

	for index := range rows {
		work, err := toDomain(&rows[index])
		if err != nil {
			return nil, ebook.Cursor{}, err
		}

		works = append(works, work)
	}

	return works, next, nil
}

// notFound is the answer to a work nobody has, one that is somebody else's,
// and one that has been tombstoned. The caller decides which of the three it
// is looking at; the reply says the same thing for all of them.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no such work in the collection").
		WithOp(op).
		WithCode(ebook.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing: the identifier is the one every annotation and every reading
// position references.
//
// The value objects are cast rather than parsed. What is in the row was
// validated by the constructor that wrote it, and re-validating here would
// make a row this node can no longer parse — a format added by a later version
// and replicated back, say — unreadable rather than merely unfamiliar.
func toDomain(row *librarydb.LibraryEbook) (*ebook.Ebook, error) {
	props := ebook.Props{
		UserID: row.UserID,
		Details: ebook.Details{
			Title: ebook.Title(row.Title),
		},
		File: ebook.File{
			Format: ebook.Format(row.Format),
			Hash:   ebook.ContentHash(row.ContentHash),
		},
		ImportedAt: row.ImportedAt,
		Revision:   revision.FromColumns(row.VectorClock, row.UpdatedAt, row.DeviceID, row.Deleted),
	}

	// The four nullable columns of the description. Absent means the file said
	// nothing, which the domain reads as the zero value of each.
	if row.Author != nil {
		props.Author = ebook.Author(*row.Author)
	}

	if row.Publisher != nil {
		props.Publisher = ebook.Publisher(*row.Publisher)
	}

	if row.Language != nil {
		props.Language = ebook.Language(*row.Language)
	}

	if row.SizeBytes != nil {
		props.Size = ebook.Size(*row.SizeBytes)
	}

	extra, err := unmarshalMetadata(row.ExtraMetadata)
	if err != nil {
		return nil, err
	}

	props.Extra = extra

	return ebook.Restore(row.ID, &props), nil
}

// marshalMetadata renders the metadata a format carried as the jsonb object
// the column is constrained to, and as NULL when there was none.
func marshalMetadata(metadata ebook.Metadata, op string) ([]byte, error) {
	if metadata.IsZero() {
		return nil, nil
	}

	encoded, err := json.Marshal(map[string]any(metadata))
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalidArgument, "the metadata could not be stored").
			WithOp(op).
			WithCode(ebook.CodeInvalidEbook).
			WithField("extra_metadata", "it must be representable as a JSON object")
	}

	return encoded, nil
}

// unmarshalMetadata reads it back. A row this node wrote always parses, so a
// failure here is a row something else wrote into the column.
func unmarshalMetadata(encoded []byte) (ebook.Metadata, error) {
	if len(encoded) == 0 {
		return nil, nil
	}

	var metadata ebook.Metadata
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the stored metadata could not be read").
			WithOp(opGetByID)
	}

	return metadata, nil
}

// optionalString renders an absent value as the NULL the column holds.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

// optionalInt64 renders an absent length as the NULL the column holds.
func optionalInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}

	return &value
}
