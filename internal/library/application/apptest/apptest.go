// Package apptest holds the doubles the library use case tests are written
// against.
//
// It is a package rather than a fixture repeated in every test file because
// the use cases of this slice depend on the same handful of ports, and a
// double written a dozen times drifts a dozen ways. It is imported only by
// tests.
//
// The doubles are fakes and not mocks: they behave, rather than record. Three
// of them behave in ways a test would otherwise have to trust the database
// for. The works repository paginates by keyset in the same order the index
// does, so a test can walk a collection page by page and find every work
// exactly once. The filings repository enforces the pair uniqueness of C06, so
// the path where a work is filed twice is a path a test can take. And the blob
// store holds bytes, so an upload followed by a download is a round trip
// rather than two assertions about call counts.
package apptest

import (
	"bytes"
	"context"
	"io"
	"sort"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
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

// Transaction runs the work directly. There is no database behind these
// doubles, so there is nothing to commit — what the fake preserves is that the
// use case still has to ask, and that the context it hands on is the one its
// repositories are called with.
type Transaction struct {
	// Err, when set, is what Within reports without running the work — for the
	// test that needs a unit that could not be opened. It is read under the
	// lock, so a parallel test may set it.
	Err error

	mu    sync.Mutex
	calls int
}

// Transaction satisfies the port the use cases hold.
var _ service.Transaction = (*Transaction)(nil)

// NewTransaction returns the fake unit of work.
func NewTransaction() *Transaction { return &Transaction{} }

// Within runs fn.
func (t *Transaction) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	t.mu.Lock()
	t.calls++
	err := t.Err
	t.mu.Unlock()

	if err != nil {
		return err
	}

	return fn(ctx)
}

// Calls is how often a unit of work was opened.
func (t *Transaction) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.calls
}

// EbookRepository is an in-memory collection that paginates the way the index
// does: most recently imported first, with the identifier breaking the tie
// between two works imported in the same microsecond.
//
// It resolves the grouping filter against a Memberships, when one was given,
// so that narrowing a page to a shelf is the same question here as in the
// EXISTS clause of the statement.
type EbookRepository struct {
	mu          sync.Mutex
	records     map[uuid.UUID]*ebook.Ebook
	memberships *MembershipRepository
}

// EbookRepository satisfies the port the use cases hold.
var _ ebook.Repository = (*EbookRepository)(nil)

// NewEbookRepository returns an empty collection. filings may be nil, in which
// case a page narrowed to a grouping is empty.
func NewEbookRepository(filings *MembershipRepository) *EbookRepository {
	return &EbookRepository{records: map[uuid.UUID]*ebook.Ebook{}, memberships: filings}
}

// Create records a work.
func (r *EbookRepository) Create(_ context.Context, work *ebook.Ebook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records[work.ID] = cloneEbook(work)

	return nil
}

// Update writes back the description, the tombstone and the revision, and
// leaves the file alone as the statement does.
func (r *EbookRepository) Update(_ context.Context, work *ebook.Ebook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[work.ID]
	if !found {
		return ebookNotFound()
	}

	updated := cloneEbook(work)
	updated.File = stored.File
	r.records[work.ID] = updated

	return nil
}

// GetByID reads a work by primary key, tombstoned or not.
func (r *EbookRepository) GetByID(_ context.Context, id uuid.UUID) (*ebook.Ebook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found {
		return nil, ebookNotFound()
	}

	return cloneEbook(stored), nil
}

// List reads one page, in the order and with the cursor semantics of the
// statement.
func (r *EbookRepository) List(
	_ context.Context, query *ebook.Query,
) ([]*ebook.Ebook, ebook.Cursor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	matching := make([]*ebook.Ebook, 0, len(r.records))

	for _, stored := range r.records {
		if stored.UserID != query.UserID || stored.IsDeleted() {
			continue
		}

		if query.CollectionID != (uuid.UUID{}) && !r.filedUnder(stored.ID, query.CollectionID) {
			continue
		}

		matching = append(matching, cloneEbook(stored))
	}

	sort.Slice(matching, func(i, j int) bool {
		return after(matching[i], matching[j])
	})

	if !query.Cursor.IsZero() {
		matching = seek(matching, query.Cursor)
	}

	var next ebook.Cursor

	if len(matching) > query.Size {
		matching = matching[:query.Size]
		last := matching[len(matching)-1]
		next = ebook.Cursor{ImportedAt: last.ImportedAt, ID: last.ID}
	}

	return matching, next, nil
}

// filedUnder answers the EXISTS clause of the statement, and answers no when
// the test gave no filings repository.
func (r *EbookRepository) filedUnder(work, grouping uuid.UUID) bool {
	if r.memberships == nil {
		return false
	}

	return r.memberships.filed(work, grouping)
}

// after orders two works as the index does: most recently imported first, with
// the identifier breaking the tie.
func after(left, right *ebook.Ebook) bool {
	if !left.ImportedAt.Equal(right.ImportedAt) {
		return left.ImportedAt.After(right.ImportedAt)
	}

	return left.ID.String() > right.ID.String()
}

// seek drops everything up to and including the row the cursor names, which is
// what the row comparison in the statement does.
func seek(works []*ebook.Ebook, cursor ebook.Cursor) []*ebook.Ebook {
	marker := ebook.Restore(cursor.ID, &ebook.Props{ImportedAt: cursor.ImportedAt})

	for index, work := range works {
		if after(marker, work) {
			return works[index:]
		}
	}

	return nil
}

// cloneEbook copies the entity, so that a caller mutating what it read does
// not reach back into the store.
func cloneEbook(work *ebook.Ebook) *ebook.Ebook {
	copied := *work
	copied.Revision.VectorClock = work.Revision.VectorClock.Clone()

	return &copied
}

// ebookNotFound is the answer to a work that is not here.
func ebookNotFound() error {
	return errs.New(errs.KindNotFound, "no such work in the collection").
		WithCode(ebook.CodeNotFound)
}

// CollectionRepository is an in-memory set of groupings, ordered by name as
// the statement is.
type CollectionRepository struct {
	mu          sync.Mutex
	records     map[uuid.UUID]*collection.Collection
	memberships *MembershipRepository
	locked      []uuid.UUID
}

// CollectionRepository satisfies the port the use cases hold.
var _ collection.Repository = (*CollectionRepository)(nil)

// NewCollectionRepository returns an empty set. filings may be nil, in which
// case a list narrowed to one work is empty.
func NewCollectionRepository(filings *MembershipRepository) *CollectionRepository {
	return &CollectionRepository{records: map[uuid.UUID]*collection.Collection{}, memberships: filings}
}

// Create records a grouping.
func (r *CollectionRepository) Create(_ context.Context, grouping *collection.Collection) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records[grouping.ID] = cloneCollection(grouping)

	return nil
}

// Update writes back the description, the tombstone and the revision.
func (r *CollectionRepository) Update(_ context.Context, grouping *collection.Collection) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[grouping.ID]; !found {
		return collectionNotFound()
	}

	r.records[grouping.ID] = cloneCollection(grouping)

	return nil
}

// GetByID reads a grouping by primary key, tombstoned or not.
func (r *CollectionRepository) GetByID(_ context.Context, id uuid.UUID) (*collection.Collection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found {
		return nil, collectionNotFound()
	}

	return cloneCollection(stored), nil
}

// GetByIDForUpdate is GetByID: there is no transaction behind these doubles,
// so there is nothing to hold. What the fake preserves is that a use case
// which must take the lock still has to ask for it, and Locked reports that it
// did.
func (r *CollectionRepository) GetByIDForUpdate(
	ctx context.Context, id uuid.UUID,
) (*collection.Collection, error) {
	r.mu.Lock()
	r.locked = append(r.locked, id)
	r.mu.Unlock()

	return r.GetByID(ctx, id)
}

// Locked is every row a caller took the lock on, in order.
func (r *CollectionRepository) Locked() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]uuid.UUID(nil), r.locked...)
}

// List reads a reader's groupings, narrowed to one work's when asked.
func (r *CollectionRepository) List(
	_ context.Context, userID, ebookID uuid.UUID,
) ([]*collection.Collection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	matching := make([]*collection.Collection, 0, len(r.records))

	for _, stored := range r.records {
		if stored.UserID != userID || stored.IsDeleted() {
			continue
		}

		if ebookID != (uuid.UUID{}) && !r.holds(ebookID, stored.ID) {
			continue
		}

		matching = append(matching, cloneCollection(stored))
	}

	sort.Slice(matching, func(i, j int) bool {
		if matching[i].Name != matching[j].Name {
			return matching[i].Name < matching[j].Name
		}

		return matching[i].ID.String() < matching[j].ID.String()
	})

	return matching, nil
}

// holds answers the EXISTS clause of the statement.
func (r *CollectionRepository) holds(work, grouping uuid.UUID) bool {
	if r.memberships == nil {
		return false
	}

	return r.memberships.filed(work, grouping)
}

// cloneCollection copies the entity, for the reason cloneEbook does.
func cloneCollection(grouping *collection.Collection) *collection.Collection {
	copied := *grouping
	copied.Revision.VectorClock = grouping.Revision.VectorClock.Clone()

	return &copied
}

// collectionNotFound is the answer to a grouping that is not here.
func collectionNotFound() error {
	return errs.New(errs.KindNotFound, "no such grouping").
		WithCode(collection.CodeNotFound)
}

// MembershipRepository is an in-memory register per (work, grouping) pair, and
// it enforces the uniqueness of that pair — which is C06, the constraint
// Quadro 20 does not have. That is what makes the path where a work is filed
// twice a path a test can take.
type MembershipRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*membership.Membership
}

// MembershipRepository satisfies the port the use cases hold.
var _ membership.Repository = (*MembershipRepository)(nil)

// NewMembershipRepository returns an empty register.
func NewMembershipRepository() *MembershipRepository {
	return &MembershipRepository{records: map[uuid.UUID]*membership.Membership{}}
}

// Create records a filing, or reports the pair as already written.
func (r *MembershipRepository) Create(_ context.Context, filing *membership.Membership) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.EbookID == filing.EbookID && stored.CollectionID == filing.CollectionID {
			return errs.New(errs.KindAlreadyExists, "that work is already filed under that grouping").
				WithCode(membership.CodeAlreadyFiled)
		}
	}

	r.records[filing.ID] = cloneMembership(filing)

	return nil
}

// Update writes back the register and the revision.
func (r *MembershipRepository) Update(_ context.Context, filing *membership.Membership) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[filing.ID]; !found {
		return membershipNotFound()
	}

	r.records[filing.ID] = cloneMembership(filing)

	return nil
}

// GetByPair reads the filing of one work under one grouping, set or cleared.
func (r *MembershipRepository) GetByPair(
	_ context.Context, ebookID, collectionID uuid.UUID,
) (*membership.Membership, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.EbookID == ebookID && stored.CollectionID == collectionID {
			return cloneMembership(stored), nil
		}
	}

	return nil, membershipNotFound()
}

// ClearForEbook tombstones every filing of one work.
func (r *MembershipRepository) ClearForEbook(
	_ context.Context, ebookID, device uuid.UUID, at time.Time,
) error {
	return r.clear(device, at, func(stored *membership.Membership) bool {
		return stored.EbookID == ebookID
	})
}

// ClearForCollection tombstones every filing under one grouping.
func (r *MembershipRepository) ClearForCollection(
	_ context.Context, collectionID, device uuid.UUID, at time.Time,
) error {
	return r.clear(device, at, func(stored *membership.Membership) bool {
		return stored.CollectionID == collectionID
	})
}

// clear tombstones every filing the predicate selects that is still set,
// through the entity, as the repository does.
func (r *MembershipRepository) clear(
	device uuid.UUID, at time.Time, selects func(*membership.Membership) bool,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, stored := range r.records {
		if !stored.IsFiled() || !selects(stored) {
			continue
		}

		cleared := cloneMembership(stored)
		cleared.Clear(device, at)
		r.records[id] = cleared
	}

	return nil
}

// Filed is every pair currently set, which is what a test asserts a shelf
// against.
func (r *MembershipRepository) Filed() []*membership.Membership {
	r.mu.Lock()
	defer r.mu.Unlock()

	filings := make([]*membership.Membership, 0, len(r.records))

	for _, stored := range r.records {
		if stored.IsFiled() {
			filings = append(filings, cloneMembership(stored))
		}
	}

	sort.Slice(filings, func(i, j int) bool {
		return filings[i].ID.String() < filings[j].ID.String()
	})

	return filings
}

// filed reports whether the pair is currently set, for the two repositories
// that resolve a filter against it.
func (r *MembershipRepository) filed(work, grouping uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.EbookID == work && stored.CollectionID == grouping {
			return stored.IsFiled()
		}
	}

	return false
}

// cloneMembership copies the entity, for the reason cloneEbook does.
func cloneMembership(filing *membership.Membership) *membership.Membership {
	copied := *filing
	copied.Revision.VectorClock = filing.Revision.VectorClock.Clone()

	return &copied
}

// membershipNotFound is the answer to a pair that has never been written.
func membershipNotFound() error {
	return errs.New(errs.KindNotFound, "that work is not filed under that grouping").
		WithCode(membership.CodeNotFound)
}

// ContentRepository is an in-memory record of what this node holds.
type ContentRepository struct {
	mu      sync.Mutex
	records map[ebook.ContentHash]*content.Content
}

// ContentRepository satisfies the port the use cases hold.
var _ content.Repository = (*ContentRepository)(nil)

// NewContentRepository returns a node that holds nothing.
func NewContentRepository() *ContentRepository {
	return &ContentRepository{records: map[ebook.ContentHash]*content.Content{}}
}

// Create records that this node holds the bytes.
func (r *ContentRepository) Create(_ context.Context, stored *content.Content) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, held := r.records[stored.Hash]; held {
		return errs.New(errs.KindAlreadyExists, "this node already holds that file").
			WithCode(content.CodeAlreadyStored)
	}

	copied := *stored
	r.records[stored.Hash] = &copied

	return nil
}

// GetByHash reads where the bytes are.
func (r *ContentRepository) GetByHash(_ context.Context, hash ebook.ContentHash) (*content.Content, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, held := r.records[hash]
	if !held {
		return nil, errs.New(errs.KindNotFound, "this node does not hold that file").
			WithCode(content.CodeNotFound)
	}

	copied := *stored

	return &copied, nil
}

// Has reports whether this node holds the bytes.
func (r *ContentRepository) Has(_ context.Context, hash ebook.ContentHash) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, held := r.records[hash]

	return held, nil
}

// BlobStore is an in-memory object store, so that an upload followed by a
// download is a round trip rather than two assertions about call counts.
type BlobStore struct {
	// PutErr and OpenErr, when set, are what the store reports instead of
	// answering — for the tests that need one that is down.
	PutErr  error
	OpenErr error

	mu      sync.Mutex
	objects map[string][]byte
	removed []string
}

// BlobStore satisfies the port the use cases hold.
var _ service.BlobStore = (*BlobStore)(nil)

// NewBlobStore returns an empty store.
func NewBlobStore() *BlobStore {
	return &BlobStore{objects: map[string][]byte{}}
}

// Bucket names where this store puts things.
func (*BlobStore) Bucket() string { return "quire-test" }

// Put stores the bytes and returns where they went.
func (s *BlobStore) Put(
	_ context.Context, blob *service.Blob, body io.Reader,
) (content.Locator, error) {
	s.mu.Lock()
	err := s.PutErr
	s.mu.Unlock()

	if err != nil {
		return content.Locator{}, err
	}

	stored, readErr := io.ReadAll(body)
	if readErr != nil {
		return content.Locator{}, readErr
	}

	at := content.Locator{Bucket: s.Bucket(), Key: service.ObjectKey(blob.Hash)}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.objects[at.Key] = stored

	return at, nil
}

// Open reads the bytes back.
func (s *BlobStore) Open(_ context.Context, at content.Locator) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.OpenErr != nil {
		return nil, s.OpenErr
	}

	stored, held := s.objects[at.Key]
	if !held {
		return nil, errs.New(errs.KindNotFound, "the object store does not have that file").
			WithCode(service.CodeBlobNotFound)
	}

	return io.NopCloser(bytes.NewReader(stored)), nil
}

// Remove deletes the object.
func (s *BlobStore) Remove(_ context.Context, at content.Locator) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.objects, at.Key)
	s.removed = append(s.removed, at.Key)

	return nil
}

// Removed is every object a caller deleted, in order. It is how a test asserts
// that an upload which could not be recorded did not leave its bytes behind.
func (s *BlobStore) Removed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.removed...)
}

// Stored is the bytes held under one digest, and whether any are.
func (s *BlobStore) Stored(hash ebook.ContentHash) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, held := s.objects[service.ObjectKey(hash)]

	return stored, held
}
