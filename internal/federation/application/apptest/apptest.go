// Package apptest holds the doubles the federation use case tests are written
// against.
//
// It is a package rather than a fixture repeated in every test file because
// the use cases of this slice depend on the same handful of ports, and a
// double written eight times drifts eight ways. It is imported only by tests.
//
// The doubles are fakes and not mocks: they behave, rather than record. The
// discovery double in particular answers out of a catalogue of documents, so
// that a test can exercise a peer that publishes no pin — a real case, and one
// no assertion about a call count would describe.
package apptest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// Discovery answers lookups out of the documents a test gave it.
type Discovery struct {
	mu        sync.Mutex
	published map[server.Domain]*server.Descriptor
	lookups   []server.Domain

	// Err, when set, is what Discover reports without answering — for the test
	// that needs a peer which is down.
	Err error
}

// Discovery satisfies the port the use cases hold.
var _ service.Discovery = (*Discovery)(nil)

// NewDiscovery returns a client that knows about nothing.
func NewDiscovery() *Discovery {
	return &Discovery{published: map[server.Domain]*server.Descriptor{}}
}

// Publish makes domain answer with descriptor, as a node that serves a
// document does.
func (d *Discovery) Publish(descriptor *server.Descriptor) {
	d.mu.Lock()
	defer d.mu.Unlock()

	stored := *descriptor
	d.published[descriptor.Domain] = &stored
}

// Discover answers with what the domain publishes.
func (d *Discovery) Discover(_ context.Context, domain server.Domain) (*server.Descriptor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lookups = append(d.lookups, domain)

	if d.Err != nil {
		return nil, d.Err
	}

	descriptor, found := d.published[domain]
	if !found {
		return nil, errs.New(errs.KindFailedPrecondition,
			"that domain does not publish a quire discovery document").
			WithCode(service.CodeNotAQuireServer)
	}

	// Copied out, so that a caller mutating what it read does not reach back
	// into the document the node publishes.
	answer := *descriptor

	return &answer, nil
}

// Lookups is every domain the client was asked about, in order. It is how a
// test asserts that a use case which should have stored something instead
// asked nobody, and the other way round.
func (d *Discovery) Lookups() []server.Domain {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]server.Domain(nil), d.lookups...)
}

// Descriptor is a node as a well-formed document describes it, which most
// tests need one of and none of them need to spell out.
func Descriptor(domain server.Domain) *server.Descriptor {
	return &server.Descriptor{
		Domain:                 domain,
		BaseURL:                server.BaseURL("https://" + domain.String()),
		JWKSURI:                server.JWKSURI("https://" + domain.String() + "/.well-known/jwks.json"),
		CertificateFingerprint: server.Fingerprint(wellknown.PinPrefix + "Zm9vYmFyCg=="),
		GRPCAuthority:          server.GRPCAuthority(domain.String() + ":9090"),
	}
}

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

// ServerRepository is an in-memory catalogue that enforces what the table
// enforces: one row per domain, and at most one row claiming to be this
// instance.
type ServerRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*server.Server
	locked  []uuid.UUID
}

// ServerRepository satisfies the port the use cases hold.
var _ server.Repository = (*ServerRepository)(nil)

// NewServerRepository returns an empty catalogue.
func NewServerRepository() *ServerRepository {
	return &ServerRepository{records: map[uuid.UUID]*server.Server{}}
}

// EnsureLocal creates or refreshes the row that says which node this is.
func (r *ServerRepository) EnsureLocal(
	_ context.Context,
	descriptor *server.Descriptor,
) (*server.Server, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.Domain != descriptor.Domain {
			continue
		}

		stored.Descriptor = *descriptor
		stored.IsLocal = true
		stored.Active = true

		return cloneServer(stored), nil
	}

	local := server.Restore(uuid.New(), &server.Props{
		Descriptor: *descriptor,
		IsLocal:    true,
		Active:     true,
	})
	r.records[local.ID] = local

	return cloneServer(local), nil
}

// Create records a peer, or reports the domain as already known.
func (r *ServerRepository) Create(_ context.Context, node *server.Server) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.Domain == node.Domain {
			return errs.New(errs.KindAlreadyExists, "that node is already in the catalogue").
				WithCode(server.CodeDomainKnown).
				WithField("domain", "it is already known here")
		}
	}

	r.records[node.ID] = cloneServer(node)

	return nil
}

// Update writes back what a refresh learned and whether the node takes part.
func (r *ServerRepository) Update(_ context.Context, node *server.Server) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[node.ID]; !found {
		return serverNotFound()
	}

	r.records[node.ID] = cloneServer(node)

	return nil
}

// Delete forgets the peer, refusing this instance as the statement does. It
// cannot see the authorizations, so the guard over those is the one the
// integration suite covers.
func (r *ServerRepository) Delete(_ context.Context, id uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found || stored.IsLocal {
		return false, nil
	}

	delete(r.records, id)

	return true, nil
}

// GetByID reads a node by primary key.
func (r *ServerRepository) GetByID(_ context.Context, id uuid.UUID) (*server.Server, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found {
		return nil, serverNotFound()
	}

	return cloneServer(stored), nil
}

// GetByIDForUpdate is GetByID: there is no transaction behind these doubles,
// so there is nothing to hold. What the fake preserves is that a use case
// which must take the lock still has to ask for it, and Locked reports that it
// did.
func (r *ServerRepository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*server.Server, error) {
	r.mu.Lock()
	r.locked = append(r.locked, id)
	r.mu.Unlock()

	return r.GetByID(ctx, id)
}

// Locked is every row a caller took the lock on, in order.
func (r *ServerRepository) Locked() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]uuid.UUID(nil), r.locked...)
}

// GetByDomain reads a node by the authority it is known as.
func (r *ServerRepository) GetByDomain(_ context.Context, domain server.Domain) (*server.Server, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.Domain == domain {
			return cloneServer(stored), nil
		}
	}

	return nil, serverNotFound()
}

// List reads the catalogue, ordered as the statement orders it.
func (r *ServerRepository) List(_ context.Context, includeInactive bool) ([]*server.Server, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found := make([]*server.Server, 0, len(r.records))

	for _, stored := range r.records {
		if !stored.Active && !includeInactive {
			continue
		}

		found = append(found, cloneServer(stored))
	}

	slices.SortFunc(found, func(a, b *server.Server) int {
		return strings.Compare(string(a.Domain), string(b.Domain))
	})

	return found, nil
}

// Count is how many nodes the catalogue holds, for a test that asserts a
// refused addition wrote nothing.
func (r *ServerRepository) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.records)
}

// cloneServer copies the entity, so that a caller mutating what it stored or
// read does not reach into the catalogue — which is what a row would not allow
// either.
func cloneServer(node *server.Server) *server.Server {
	copied := *node

	return &copied
}

// serverNotFound is the answer to a node that is not in the catalogue.
func serverNotFound() error {
	return errs.New(errs.KindNotFound, "no such node in the catalogue").WithCode(server.CodeNotFound)
}

// ReplicaRepository is an in-memory authorization repository, with the one row
// per pair the unique constraint enforces.
type ReplicaRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*replica.Replica
}

// ReplicaRepository satisfies the port the use cases hold.
var _ replica.Repository = (*ReplicaRepository)(nil)

// NewReplicaRepository returns an empty repository.
func NewReplicaRepository() *ReplicaRepository {
	return &ReplicaRepository{records: map[uuid.UUID]*replica.Replica{}}
}

// Create grants a permission that did not exist.
func (r *ReplicaRepository) Create(_ context.Context, authorization *replica.Replica) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.UserID == authorization.UserID && stored.ServerID == authorization.ServerID {
			return errs.New(errs.KindAlreadyExists, "that node is already authorized for this reader").
				WithCode(replica.CodePairKnown).
				WithField("server_id", "one row holds the whole history of this decision, and it exists")
		}
	}

	r.records[authorization.ID] = cloneReplica(authorization)

	return nil
}

// Update writes back the three columns a decision changes.
func (r *ReplicaRepository) Update(_ context.Context, authorization *replica.Replica) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[authorization.ID]; !found {
		return replicaNotFound()
	}

	r.records[authorization.ID] = cloneReplica(authorization)

	return nil
}

// GetByPair reads the authorization of one reader for one node.
func (r *ReplicaRepository) GetByPair(
	_ context.Context,
	userID, serverID uuid.UUID,
) (*replica.Replica, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.UserID == userID && stored.ServerID == serverID {
			return cloneReplica(stored), nil
		}
	}

	return nil, replicaNotFound()
}

// ListByUser reads a reader's authorizations, ordered as the statement orders
// them.
func (r *ReplicaRepository) ListByUser(
	_ context.Context,
	userID uuid.UUID,
	includeInactive bool,
) ([]*replica.Replica, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found := make([]*replica.Replica, 0, len(r.records))

	for _, stored := range r.records {
		if stored.UserID != userID || (!stored.Active && !includeInactive) {
			continue
		}

		found = append(found, cloneReplica(stored))
	}

	slices.SortFunc(found, func(a, b *replica.Replica) int {
		if byTime := b.AuthorizedAt.Compare(a.AuthorizedAt); byTime != 0 {
			return byTime
		}

		return a.ID.Compare(b.ID)
	})

	return found, nil
}

// CountActiveForServer is how many readers still allow the node to hold a copy.
func (r *ReplicaRepository) CountActiveForServer(_ context.Context, serverID uuid.UUID) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var count int64

	for _, stored := range r.records {
		if stored.ServerID == serverID && stored.Active {
			count++
		}
	}

	return count, nil
}

// cloneReplica copies the entity, for the reason cloneServer does.
func cloneReplica(authorization *replica.Replica) *replica.Replica {
	copied := *authorization

	return &copied
}

// replicaNotFound is the answer to an authorization that is not here.
func replicaNotFound() error {
	return errs.New(errs.KindNotFound, "that node holds nothing of this reader's").
		WithCode(replica.CodeNotFound)
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
