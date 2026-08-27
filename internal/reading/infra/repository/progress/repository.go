// Package progress is the PostgreSQL adapter of the reading positions
// repository: it satisfies the port declared in
// internal/reading/domain/progress and is the only place that knows
// reading.progress exists.
package progress

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/persist/readingdb"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opCreate       = "reading/progress: create"
	opUpdate       = "reading/progress: update"
	opGetByPair    = "reading/progress: get by pair"
	opListForEbook = "reading/progress: list for ebook"
)

// constraintPair is the name of the uniqueness rule on the (work, device)
// pair, as it appears in the driver error. It is what tells a position that
// already exists from any other write failure — and the rule itself is C05,
// which Quadro 21 does not have.
const constraintPair = "progress_ebook_device_key"

// Repository reads and writes where each device stopped, in PostgreSQL.
type Repository struct {
	manager *persist.Manager
}

// Repository satisfies the port the use cases hold.
var _ progress.Repository = (*Repository)(nil)

// New returns a repository over manager.
func New(manager *persist.Manager) *Repository {
	return &Repository{manager: manager}
}

// queries binds the generated statements to whatever ctx is running in.
func (r *Repository) queries(ctx context.Context) *readingdb.Queries {
	return readingdb.New(r.manager.Executor(ctx))
}

// Create records a first position, naming the pair rule when it was the one
// broken.
//
// The rule is broken by two calls from the same device crossing — a reply lost
// on a mobile network and a client that retried — and the caller answers it by
// reading the row and moving it. That is why the refusal is a kind of its own
// rather than an internal error: it says which of the two answers to take.
func (r *Repository) Create(ctx context.Context, position *progress.Progress) error {
	err := r.queries(ctx).CreateProgress(ctx, readingdb.CreateProgressParams{
		ID:          position.ID,
		EbookID:     position.EbookID,
		DeviceID:    position.DeviceID,
		Locator:     position.Locator.String(),
		Percent:     optionalPercent(position.Percent),
		VectorClock: position.Version.VectorClock.Compact(),
		UpdatedAt:   position.Version.UpdatedAt,
	})

	if persist.IsUniqueViolation(err, constraintPair) {
		return errs.Wrap(err, errs.KindAlreadyExists, "that device already has a position in that work").
			WithOp(opCreate).
			WithCode(progress.CodeAlreadyExists).
			WithField("ebook_id", "the pair already has a row, which the caller should move")
	}

	return persist.Classify(err, opCreate)
}

// Update writes back the position and the version.
//
// Neither half of the natural key is written. A position belongs to one work
// and one device for as long as it exists.
func (r *Repository) Update(ctx context.Context, position *progress.Progress) error {
	rows, err := r.queries(ctx).UpdateProgress(ctx, readingdb.UpdateProgressParams{
		ID:          position.ID,
		Locator:     position.Locator.String(),
		Percent:     optionalPercent(position.Percent),
		VectorClock: position.Version.VectorClock.Compact(),
		UpdatedAt:   position.Version.UpdatedAt,
	})
	if err != nil {
		return persist.Classify(err, opUpdate)
	}

	// An UPDATE that matched nothing is not an error to PostgreSQL, and here
	// it is a work that was deleted between the read and the write: the row
	// went with it, by the cascade the schema declares.
	if rows == 0 {
		return notFound(nil, opUpdate)
	}

	return nil
}

// GetByPair reads where one device stopped in one work.
func (r *Repository) GetByPair(
	ctx context.Context, ebookID, deviceID uuid.UUID,
) (*progress.Progress, error) {
	row, err := r.queries(ctx).GetProgressByPair(ctx, readingdb.GetProgressByPairParams{
		EbookID:  ebookID,
		DeviceID: deviceID,
	})
	if err != nil {
		if persist.IsNoRows(err) {
			return nil, notFound(err, opGetByPair)
		}

		return nil, persist.Classify(err, opGetByPair)
	}

	return toDomain(&row), nil
}

// ListForEbook reads every device's position in one work.
func (r *Repository) ListForEbook(
	ctx context.Context, ebookID uuid.UUID,
) ([]*progress.Progress, error) {
	rows, err := r.queries(ctx).ListProgressForEbook(ctx, ebookID)
	if err != nil {
		return nil, persist.Classify(err, opListForEbook)
	}

	positions := make([]*progress.Progress, 0, len(rows))

	for index := range rows {
		positions = append(positions, toDomain(&rows[index]))
	}

	return positions, nil
}

// notFound is the answer to a work this device has never opened, and to one
// that no longer exists. The caller decides which of the two it is looking at.
func notFound(cause error, op string) error {
	return errs.Wrap(cause, errs.KindNotFound, "no reading position for that device in that work").
		WithOp(op).
		WithCode(progress.CodeNotFound)
}

// toDomain rebuilds the entity from the row, restoring rather than
// constructing.
//
// The proportion is cast rather than validated. What is in the row was checked
// by the constructor that wrote it and by progress_percent_range, and a value
// re-refused here would make a row unreadable rather than merely out of range.
func toDomain(row *readingdb.ReadingProgress) *progress.Progress {
	props := progress.Props{
		EbookID:  row.EbookID,
		DeviceID: row.DeviceID,
		Position: progress.Position{Locator: locator.Locator(row.Locator)},
		Version: crdt.Version{
			VectorClock: row.VectorClock,
			UpdatedAt:   row.UpdatedAt,
		},
	}

	// NULL is a client that could not compute a proportion, which is a
	// different claim from a reader who has read none of the work.
	if row.Percent != nil {
		props.Percent = progress.RestorePercent(*row.Percent)
	}

	return progress.Restore(row.ID, &props)
}

// optionalPercent renders an absent proportion as the NULL the column holds.
func optionalPercent(percent progress.Percent) *float64 {
	if !percent.IsKnown() {
		return nil
	}

	value := percent.Float64()

	return &value
}
