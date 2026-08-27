// Package content is the PostgreSQL adapter of the stored-files repository: it
// satisfies the port declared in internal/library/domain/content and is the
// only place that knows library.ebook_contents exists.
package content

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/persist/librarydb"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate    = "library/content: create"
	opGetByHash = "library/content: get by hash"
	opHas       = "library/content: has"
)

// constraintHash is the primary key of the table, as it appears in the driver
// error. It is what tells a file this node already holds from any other write
// failure.
const constraintHash = "ebook_contents_pkey"

// Repository reads and writes what this node holds, in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ content.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *librarydb.Queries {
	return librarydb.New(r.manager.Executor(ctx))
}

// Create records that this node holds the bytes.
//
// A digest the table already has is reported as such rather than as a plain
// write failure, because it is the ordinary outcome of two uploads of the same
// file racing each other — and the answer to it is that the file is here,
// which is what the caller wanted.
func (r *Repository) Create(ctx context.Context, stored *content.Content) error {
	err := r.queries(ctx).CreateContent(ctx, librarydb.CreateContentParams{
		ContentHash:   stored.Hash.String(),
		SizeBytes:     stored.Size,
		MediaType:     stored.MediaType.String(),
		StorageBucket: stored.Bucket,
		StorageKey:    stored.Key,
		CreatedAt:     stored.StoredAt,
	})

	if persist.IsUniqueViolation(err, constraintHash) {
		return errs.Wrap(err, errs.KindAlreadyExists, "this node already holds that file").
			WithOp(opCreate).
			WithCode(content.CodeAlreadyStored).
			WithField("content_hash", "the digest is the key, so the bytes are already here")
	}

	return persist.Classify(err, opCreate)
}

// GetByHash reads where the bytes are.
func (r *Repository) GetByHash(ctx context.Context, hash ebook.ContentHash) (*content.Content, error) {
	row, err := r.queries(ctx).GetContentByHash(ctx, hash.String())
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err)
		}

		return nil, persist.Classify(err, opGetByHash)
	}

	return toDomain(&row), nil
}

// Has reports whether this node holds the bytes.
func (r *Repository) Has(ctx context.Context, hash ebook.ContentHash) (bool, error) {
	held, err := r.queries(ctx).HasContent(ctx, hash.String())
	if err != nil {
		return false, persist.Classify(err, opHas)
	}

	return held, nil
}

// notFound is the answer to a file this node does not hold, which is a
// legitimate state and not only an error: a node replicating a reader without
// their files is in it for every work they have.
func notFound(cause error) error {
	return errs.Wrap(cause, errs.KindNotFound, "this node does not hold that file").
		WithOp(opGetByHash).
		WithCode(content.CodeNotFound)
}

// toDomain rebuilds the entity from the row.
func toDomain(row *librarydb.LibraryEbookContent) *content.Content {
	props := content.Props{
		Size:      row.SizeBytes,
		MediaType: content.MediaType(row.MediaType),
		Locator:   content.Locator{Bucket: row.StorageBucket, Key: row.StorageKey},
		StoredAt:  row.CreatedAt,
	}

	return content.Restore(ebook.ContentHash(row.ContentHash), &props)
}
