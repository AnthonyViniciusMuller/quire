// Package apptest holds the doubles the reading use case tests are written
// against.
//
// It is a package rather than a fixture repeated in every test file because the
// use cases of this slice depend on the same handful of ports, and a double
// written a dozen times drifts a dozen ways. It is imported only by tests.
//
// The doubles are fakes and not mocks: they behave, rather than record. Two of
// them behave in ways a test would otherwise have to trust the database for.
// The marks repository paginates by identifier in the same order the index
// does, so a test can walk every note in a book page by page and find each one
// exactly once. The positions repository enforces the pair uniqueness of C05,
// so the path where two calls from one device cross is a path a test can take.
//
// The works double is the one that is not a repository. It stands for the
// library slice, which is where this slice establishes whose a mark is, and it
// is a set of visible works rather than a set of rows — the port answers one
// question and the fake answers it the same way.
package apptest

import (
	"context"
	"sort"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// Clock is a clock that does not move unless a test moves it.
type Clock struct {
	mu      sync.Mutex
	instant time.Time
}

// Clock satisfies the port the use cases hold.
var _ service.Clock = (*Clock)(nil)

// NewClock returns a clock stopped at instant.
func NewClock(instant time.Time) *Clock { return &Clock{instant: instant} }

// Now is the instant the clock is stopped at.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.instant
}

// Advance moves the clock forward, for a test that needs two instants.
func (c *Clock) Advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.instant = c.instant.Add(by)
}

// Works is the library slice as this one sees it: the set of works a reader may
// write in.
//
// It holds the pair and not the row, because the port asks about the pair. A
// work another reader owns, a work that was tombstoned and a work that never
// existed are all simply absent, which is exactly what the adapter answers for
// all three.
type Works struct {
	mu      sync.Mutex
	visible map[uuid.UUID]uuid.UUID
}

// Works satisfies the port the use cases hold.
var _ service.Works = (*Works)(nil)

// NewWorks returns a library holding nothing.
func NewWorks() *Works { return &Works{visible: map[uuid.UUID]uuid.UUID{}} }

// Add records that the work is in the reader's collection and not tombstoned.
func (w *Works) Add(ebookID, userID uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.visible[ebookID] = userID
}

// Remove records that the work is gone, which is what tombstoning one looks
// like from here.
func (w *Works) Remove(ebookID uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.visible, ebookID)
}

// Visible reports nil when the work is in the reader's collection.
func (w *Works) Visible(_ context.Context, ebookID, userID uuid.UUID) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// The kind is what the use cases branch on, and it is all the fake
	// promises. The code the client sees is the library slice's, which the
	// real adapter takes from the entity that owns it — a copy of the string
	// here would be a second definition of it in the slice the port exists to
	// keep out.
	if owner, found := w.visible[ebookID]; !found || owner != userID {
		return errs.New(errs.KindNotFound, "no such work in the collection")
	}

	return nil
}

// AnnotationRepository is an in-memory set of marks that paginates the way the
// index does: by identifier, ascending.
type AnnotationRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*annotation.Annotation
}

// AnnotationRepository satisfies the port the use cases hold.
var _ annotation.Repository = (*AnnotationRepository)(nil)

// NewAnnotationRepository returns an empty set.
func NewAnnotationRepository() *AnnotationRepository {
	return &AnnotationRepository{records: map[uuid.UUID]*annotation.Annotation{}}
}

// Create records a mark.
func (r *AnnotationRepository) Create(_ context.Context, mark *annotation.Annotation) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records[mark.ID] = cloneAnnotation(mark)

	return nil
}

// Update writes back the mark, the tombstone and the revision, and leaves the
// work alone as the statement does.
func (r *AnnotationRepository) Update(_ context.Context, mark *annotation.Annotation) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[mark.ID]
	if !found {
		return annotationNotFound()
	}

	updated := cloneAnnotation(mark)
	updated.EbookID = stored.EbookID
	r.records[mark.ID] = updated

	return nil
}

// GetByID reads a mark by primary key, tombstoned or not.
func (r *AnnotationRepository) GetByID(
	_ context.Context, id uuid.UUID,
) (*annotation.Annotation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found {
		return nil, annotationNotFound()
	}

	return cloneAnnotation(stored), nil
}

// List reads one page, in the order and with the cursor semantics of the
// statement.
func (r *AnnotationRepository) List(
	_ context.Context, query *annotation.Query,
) ([]*annotation.Annotation, annotation.Cursor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	matching := make([]*annotation.Annotation, 0, len(r.records))

	for _, stored := range r.records {
		if stored.EbookID != query.EbookID || stored.IsDeleted() {
			continue
		}

		if !query.Cursor.IsZero() && stored.ID.String() <= query.Cursor.ID.String() {
			continue
		}

		matching = append(matching, cloneAnnotation(stored))
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].ID.String() < matching[j].ID.String()
	})

	var next annotation.Cursor

	if len(matching) > query.Size {
		matching = matching[:query.Size]
		next = annotation.Cursor{ID: matching[len(matching)-1].ID}
	}

	return matching, next, nil
}

// cloneAnnotation copies the entity, so that a caller mutating what it read
// does not reach back into the store.
func cloneAnnotation(mark *annotation.Annotation) *annotation.Annotation {
	copied := *mark
	copied.Revision.VectorClock = mark.Revision.VectorClock.Clone()

	return &copied
}

// annotationNotFound is the answer to a mark that is not here.
func annotationNotFound() error {
	return errs.New(errs.KindNotFound, "no such annotation").
		WithCode(annotation.CodeNotFound)
}

// ProgressRepository is an in-memory set of reading positions that enforces the
// pair uniqueness of C05, so that the path where two calls from one device
// cross is a path a test can take.
type ProgressRepository struct {
	// BeforeCreate, when set, runs once just before the next create is
	// attempted. It is how a test puts a crossing call between the read and
	// the write, which is the only way to reach the path where the constraint
	// refuses an insert the caller had every reason to make.
	BeforeCreate func()

	mu      sync.Mutex
	records map[uuid.UUID]*progress.Progress
}

// ProgressRepository satisfies the port the use cases hold.
var _ progress.Repository = (*ProgressRepository)(nil)

// NewProgressRepository returns an empty set.
func NewProgressRepository() *ProgressRepository {
	return &ProgressRepository{records: map[uuid.UUID]*progress.Progress{}}
}

// Create records a first position, refusing a pair that already has one.
func (r *ProgressRepository) Create(_ context.Context, position *progress.Progress) error {
	if crossing := r.BeforeCreate; crossing != nil {
		r.BeforeCreate = nil
		crossing()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.find(position.EbookID, position.DeviceID) != nil {
		return errs.New(errs.KindAlreadyExists, "that device already has a position in that work").
			WithCode(progress.CodeAlreadyExists)
	}

	r.records[position.ID] = cloneProgress(position)

	return nil
}

// Update writes back the position and the version.
func (r *ProgressRepository) Update(_ context.Context, position *progress.Progress) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[position.ID]; !found {
		return progressNotFound()
	}

	r.records[position.ID] = cloneProgress(position)

	return nil
}

// GetByPair reads where one device stopped in one work.
func (r *ProgressRepository) GetByPair(
	_ context.Context, ebookID, deviceID uuid.UUID,
) (*progress.Progress, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored := r.find(ebookID, deviceID)
	if stored == nil {
		return nil, progressNotFound()
	}

	return cloneProgress(stored), nil
}

// ListForEbook reads every device's position in one work, ordered by device as
// the statement is.
func (r *ProgressRepository) ListForEbook(
	_ context.Context, ebookID uuid.UUID,
) ([]*progress.Progress, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	matching := make([]*progress.Progress, 0, len(r.records))

	for _, stored := range r.records {
		if stored.EbookID == ebookID {
			matching = append(matching, cloneProgress(stored))
		}
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].DeviceID.String() < matching[j].DeviceID.String()
	})

	return matching, nil
}

// find is the pair lookup the unique constraint indexes. The caller holds the
// lock.
func (r *ProgressRepository) find(ebookID, deviceID uuid.UUID) *progress.Progress {
	for _, stored := range r.records {
		if stored.EbookID == ebookID && stored.DeviceID == deviceID {
			return stored
		}
	}

	return nil
}

// cloneProgress copies the entity, so that a caller mutating what it read does
// not reach back into the store.
func cloneProgress(position *progress.Progress) *progress.Progress {
	copied := *position
	copied.Version.VectorClock = position.Version.VectorClock.Clone()

	return &copied
}

// progressNotFound is the answer to a work a device has never opened.
func progressNotFound() error {
	return errs.New(errs.KindNotFound, "no reading position for that device in that work").
		WithCode(progress.CodeNotFound)
}
