// Package apptest holds the doubles the use case tests are written against.
//
// It is a package rather than a fixture repeated in every test file because the
// use cases of this slice depend on the same handful of ports, and a double
// written eight times drifts eight ways. It is imported only by tests.
//
// The doubles are fakes and not mocks: they behave, rather than record. The
// reader repository in particular enforces the uniqueness of RN09, so that a
// test can exercise the duplicate registration path — the one an index decides
// in production — without a database.
package apptest

import (
	"context"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// Clock is a clock that does not move unless a test moves it.
type Clock struct {
	mu      sync.Mutex
	instant time.Time
}

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

// HashService is a hashing port that is reversible on purpose, so that a test
// can assert which password was stored without hashing anything.
type HashService struct{}

// NewHashService returns the fake hasher.
func NewHashService() *HashService { return &HashService{} }

// hashPrefix is what a fake digest is made of.
const hashPrefix = "hashed:"

// absentDigest is the digest nothing matches, standing in for the real one.
const absentDigest = "hashed:\x00no reader"

// Hash returns a digest a test can read.
func (h *HashService) Hash(plaintext string) (string, error) { return hashPrefix + plaintext, nil }

// Verify reports whether plaintext produced digest.
func (h *HashService) Verify(plaintext, digest string) (bool, error) {
	return digest == hashPrefix+plaintext, nil
}

// AbsentDigest is the digest no password matches.
func (h *HashService) AbsentDigest() string { return absentDigest }

// LocalServer answers with a fixed identity, as a configured node does.
type LocalServer struct {
	// ServerID is the row every reader registered here points at.
	ServerID uuid.UUID
	// ServerDomain is the second half of the identifiers it issues.
	ServerDomain user.ServerDomain
	// Err, when set, is what ID reports — for the test that needs a node whose
	// catalogue is unreachable.
	Err error
}

// NewLocalServer returns a node identified by domain.
func NewLocalServer(domain user.ServerDomain) *LocalServer {
	return &LocalServer{ServerID: uuid.New(), ServerDomain: domain}
}

// ID is the node's row in the catalogue.
func (l *LocalServer) ID(_ context.Context) (uuid.UUID, error) {
	if l.Err != nil {
		return uuid.UUID{}, l.Err
	}

	return l.ServerID, nil
}

// Domain is the second half of every identifier this node issues.
func (l *LocalServer) Domain() user.ServerDomain { return l.ServerDomain }

// UserRepository is an in-memory reader repository that enforces the two
// uniqueness rules of RN09, and reports the same coded errors the PostgreSQL
// one does.
type UserRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*user.User
}

// NewUserRepository returns an empty repository.
func NewUserRepository() *UserRepository {
	return &UserRepository{records: map[uuid.UUID]*user.User{}}
}

// Create inserts the reader, or reports which uniqueness rule it broke.
func (r *UserRepository) Create(_ context.Context, record *user.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.OriginServerID != record.OriginServerID {
			continue
		}

		if stored.LocalName == record.LocalName {
			return errs.New(errs.KindAlreadyExists, "that name is already taken on this server").
				WithCode(user.CodeLocalNameTaken).
				WithField("local_name", "it belongs to another reader here")
		}

		if !stored.Email.IsZero() && stored.Email.Fold() == record.Email.Fold() {
			return errs.New(errs.KindAlreadyExists, "that address is already registered on this server").
				WithCode(user.CodeEmailRegistered).
				WithField("email", "it is already in use here")
		}
	}

	r.records[record.ID] = clone(record)

	return nil
}

// Update writes back the mutable fields.
func (r *UserRepository) Update(_ context.Context, record *user.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[record.ID]; !found {
		return notFound()
	}

	r.records[record.ID] = clone(record)

	return nil
}

// Delete removes the reader.
func (r *UserRepository) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[id]; !found {
		return notFound()
	}

	delete(r.records, id)

	return nil
}

// GetByID reads a reader by primary key.
func (r *UserRepository) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found {
		return nil, notFound()
	}

	return clone(stored), nil
}

// GetByLocalName reads a reader by the pair RN09 makes unique.
func (r *UserRepository) GetByLocalName(
	_ context.Context,
	originServerID uuid.UUID,
	localName user.LocalName,
) (*user.User, error) {
	return r.find(func(stored *user.User) bool {
		return stored.OriginServerID == originServerID && stored.LocalName == localName
	})
}

// GetByEmail reads a reader by address, folding case as the index does.
func (r *UserRepository) GetByEmail(
	_ context.Context,
	originServerID uuid.UUID,
	email user.Email,
) (*user.User, error) {
	return r.find(func(stored *user.User) bool {
		return stored.OriginServerID == originServerID &&
			!stored.Email.IsZero() && stored.Email.Fold() == email.Fold()
	})
}

// Count is how many readers the repository holds, for a test that asserts a
// failed registration wrote nothing.
func (r *UserRepository) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.records)
}

// find returns the first reader matching predicate.
func (r *UserRepository) find(predicate func(*user.User) bool) (*user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if predicate(stored) {
			return clone(stored), nil
		}
	}

	return nil, notFound()
}

// clone copies the entity, so that a caller mutating what it stored or read
// does not reach into the repository — which is what a row would not allow
// either.
func clone(record *user.User) *user.User {
	copied := *record

	return &copied
}

// notFound is the answer to a reader who is not here, in the vocabulary the
// PostgreSQL repository uses.
func notFound() error {
	return errs.New(errs.KindNotFound, "no such reader on this server").WithCode(user.CodeNotFound)
}
