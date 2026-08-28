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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
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

	if err := r.conflict(record); err != nil {
		return err
	}

	r.records[record.ID] = clone(record)

	return nil
}

// Update writes back the mutable fields.
//
// It checks uniqueness too, because the index does: RN09 holds however the
// address got there, and a fake that only checked on insert would let a use
// case take another reader's address in a test and fail in production.
func (r *UserRepository) Update(_ context.Context, record *user.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[record.ID]; !found {
		return notFound()
	}

	if err := r.conflict(record); err != nil {
		return err
	}

	r.records[record.ID] = clone(record)

	return nil
}

// conflict reports which of the two uniqueness rules of RN09 record breaks
// against the readers already stored, ignoring the reader's own row.
func (r *UserRepository) conflict(record *user.User) error {
	for _, stored := range r.records {
		if stored.ID == record.ID || stored.OriginServerID != record.OriginServerID {
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

// AuthService issues tokens a test can read, and digests it can predict.
type AuthService struct {
	mu     sync.Mutex
	issued int

	// AccessTTL and RefreshTTL are the lifetimes the fake stamps, so that a
	// test can assert which of the two a credential was given.
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	// RecoveryTTL is the shorter one UC08 uses.
	RecoveryTTL time.Duration
}

// NewAuthService returns the fake with the lifetimes the node defaults to.
func NewAuthService() *AuthService {
	return &AuthService{
		AccessTTL:   15 * time.Minute,
		RefreshTTL:  720 * time.Hour,
		RecoveryTTL: time.Hour,
	}
}

// The shapes the fake's values take, so that a test can recognize one.
const (
	accessPrefix   = "access:"
	refreshPrefix  = "refresh:"
	recoveryPrefix = "recovery:"
	digestPrefix   = "digest:"
)

// IssueAccess signs nothing: it returns a value naming the reader and the
// device, which is what the real claims assert and what a test checks.
func (a *AuthService) IssueAccess(userID, deviceID uuid.UUID, now time.Time) (string, service.Claims, error) {
	claims := service.Claims{
		TokenID:   uuid.New(),
		UserID:    userID,
		DeviceID:  deviceID,
		Issuer:    "https://quire-a.example",
		Audience:  "quire-a.example",
		IssuedAt:  now,
		ExpiresAt: now.Add(a.AccessTTL),
	}

	return accessPrefix + userID.String() + ":" + deviceID.String() + ":" + claims.TokenID.String(), claims, nil
}

// VerifyAccess reads back what IssueAccess wrote, without a signature.
func (a *AuthService) VerifyAccess(token string, now time.Time) (service.Claims, error) {
	rest, found := strings.CutPrefix(token, accessPrefix)
	if !found {
		return service.Claims{}, errs.New(errs.KindUnauthenticated, "that token could not be read").
			WithCode(service.CodeTokenInvalid)
	}

	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		return service.Claims{}, errs.New(errs.KindUnauthenticated, "that token could not be read").
			WithCode(service.CodeTokenInvalid)
	}

	userID, err := uuid.Parse(parts[0])
	if err != nil {
		return service.Claims{}, errs.New(errs.KindUnauthenticated, "that token names no reader").
			WithCode(service.CodeTokenInvalid)
	}

	deviceID, err := uuid.Parse(parts[1])
	if err != nil {
		return service.Claims{}, errs.New(errs.KindUnauthenticated, "that token names no device").
			WithCode(service.CodeTokenInvalid)
	}

	tokenID, err := uuid.Parse(parts[2])
	if err != nil {
		return service.Claims{}, errs.New(errs.KindUnauthenticated, "that token has no identifier").
			WithCode(service.CodeTokenInvalid)
	}

	return service.Claims{
		TokenID:  tokenID,
		UserID:   userID,
		DeviceID: deviceID,
		Issuer:   "https://quire-a.example",
		Audience: "quire-a.example",
		// The fake carries no expiry inside the token, so a test that needs an
		// expired one substitutes an error rather than waiting.
		ExpiresAt: now.Add(a.AccessTTL),
	}, nil
}

// IssueRefresh mints a distinct credential every call, as the real one does.
func (a *AuthService) IssueRefresh(now time.Time) (service.Secret, error) {
	return a.mint(refreshPrefix, now, a.RefreshTTL), nil
}

// IssueRecovery mints the shorter-lived credential of UC08.
func (a *AuthService) IssueRecovery(now time.Time) (service.Secret, error) {
	return a.mint(recoveryPrefix, now, a.RecoveryTTL), nil
}

// mint is what both minting methods share.
func (a *AuthService) mint(prefix string, now time.Time, ttl time.Duration) service.Secret {
	a.mu.Lock()
	a.issued++
	value := prefix + strconv.Itoa(a.issued)
	a.mu.Unlock()

	return service.Secret{Value: value, Digest: a.DigestOf(value), ExpiresAt: now.Add(ttl)}
}

// DigestOf is reversible on purpose, so that a test can say which credential a
// row holds.
func (a *AuthService) DigestOf(presented string) string { return digestPrefix + presented }

// JWKS is a document with no keys in it; nothing under test reads it.
func (a *AuthService) JWKS() []byte { return []byte(`{"keys":[]}`) }

// DeviceRepository is an in-memory device repository.
type DeviceRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*device.Device
}

// NewDeviceRepository returns an empty repository.
func NewDeviceRepository() *DeviceRepository {
	return &DeviceRepository{records: map[uuid.UUID]*device.Device{}}
}

// Create binds the device, refusing an identifier this repository already
// holds.
//
// The primary key is what enforces that in production, and it matters here
// because one call chooses the identifier rather than minting it: UC16 adopts
// the devices a reader is bringing with the identifiers they already hold
// (C11), and two devices under one identifier would make two histories
// indistinguishable.
func (r *DeviceRepository) Create(_ context.Context, appliance *device.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, held := r.records[appliance.ID]; held {
		return errs.New(errs.KindAlreadyExists, "that device identifier is already bound").
			WithOp("apptest/device: create").
			WithCode(device.CodeNotFound)
	}

	r.records[appliance.ID] = cloneDevice(appliance)

	return nil
}

// Update writes back the mutable fields.
func (r *DeviceRepository) Update(_ context.Context, appliance *device.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.records[appliance.ID]; !found {
		return deviceNotFound()
	}

	r.records[appliance.ID] = cloneDevice(appliance)

	return nil
}

// GetByID reads a device by primary key, bound or not.
func (r *DeviceRepository) GetByID(_ context.Context, id uuid.UUID) (*device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found {
		return nil, deviceNotFound()
	}

	return cloneDevice(stored), nil
}

// ListByUser reads the devices of a reader, ordered as the statement orders
// them.
func (r *DeviceRepository) ListByUser(
	_ context.Context,
	userID uuid.UUID,
	includeInactive bool,
) ([]*device.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found := make([]*device.Device, 0, len(r.records))

	for _, stored := range r.records {
		if stored.UserID != userID || (!stored.Active && !includeInactive) {
			continue
		}

		found = append(found, cloneDevice(stored))
	}

	slices.SortFunc(found, func(a, b *device.Device) int {
		if byName := strings.Compare(string(a.Name), string(b.Name)); byName != 0 {
			return byName
		}

		return a.ID.Compare(b.ID)
	})

	return found, nil
}

// Count is how many devices the repository holds.
func (r *DeviceRepository) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.records)
}

// cloneDevice copies the entity, for the reason clone does.
func cloneDevice(appliance *device.Device) *device.Device {
	copied := *appliance

	return &copied
}

// deviceNotFound is the answer to a device that is not here.
func deviceNotFound() error {
	return errs.New(errs.KindNotFound, "no such device").WithCode(device.CodeNotFound)
}

// CredentialRepository is an in-memory credential repository, with the consume
// semantics the statement has: spending one that is already spent fails.
type CredentialRepository struct {
	mu      sync.Mutex
	records map[uuid.UUID]*credential.Credential
}

// NewCredentialRepository returns an empty repository.
func NewCredentialRepository() *CredentialRepository {
	return &CredentialRepository{records: map[uuid.UUID]*credential.Credential{}}
}

// Create stores the credential.
func (r *CredentialRepository) Create(_ context.Context, issued *credential.Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records[issued.ID] = cloneCredential(issued)

	return nil
}

// GetByTokenHash reads the credential a caller presented.
func (r *CredentialRepository) GetByTokenHash(_ context.Context, tokenHash string) (*credential.Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.TokenHash == tokenHash {
			return cloneCredential(stored), nil
		}
	}

	return nil, errs.New(errs.KindNotFound, "that credential is not valid").
		WithCode(credential.CodeNotFound)
}

// Consume spends the credential, refusing one that is already spent.
func (r *CredentialRepository) Consume(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, found := r.records[id]
	if !found || stored.Consumed {
		return errs.New(errs.KindConflict, "that credential has already been used").
			WithCode(credential.CodeSpent)
	}

	stored.Consumed = true

	return nil
}

// ConsumeForDevice revokes every unconsumed credential of a device.
func (r *CredentialRepository) ConsumeForDevice(_ context.Context, deviceID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.BelongsToDevice(deviceID) {
			stored.Consumed = true
		}
	}

	return nil
}

// ConsumeForUser revokes every unconsumed credential of a reader, of one kind.
func (r *CredentialRepository) ConsumeForUser(_ context.Context, userID uuid.UUID, kind credential.Kind) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.records {
		if stored.UserID == userID && stored.Kind == kind {
			stored.Consumed = true
		}
	}

	return nil
}

// DeleteExpired removes the credentials that expired before the instant given.
func (r *CredentialRepository) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var removed int64

	for id, stored := range r.records {
		if stored.ExpiresAt.Before(before) {
			delete(r.records, id)
			removed++
		}
	}

	return removed, nil
}

// Live is how many credentials of a kind are still usable at the instant given,
// for a test that asserts what a revocation reached.
func (r *CredentialRepository) Live(kind credential.Kind, at time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	var live int

	for _, stored := range r.records {
		if stored.Kind == kind && stored.Usable(at) {
			live++
		}
	}

	return live
}

// cloneCredential copies the entity, for the reason clone does.
func cloneCredential(issued *credential.Credential) *credential.Credential {
	copied := *issued

	return &copied
}

// Mailer records what it was asked to deliver instead of delivering it.
type Mailer struct {
	mu      sync.Mutex
	sent    []service.RecoveryMessage
	notices []service.EmailChangedMessage

	// Err, when set, is what either method reports — for the tests that need a
	// transport that is down.
	Err error
}

// NewMailer returns the fake transport.
func NewMailer() *Mailer { return &Mailer{} }

// SendPasswordRecovery records the message.
func (m *Mailer) SendPasswordRecovery(_ context.Context, message service.RecoveryMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Err != nil {
		return m.Err
	}

	m.sent = append(m.sent, message)

	return nil
}

// SendEmailChanged records the notice.
func (m *Mailer) SendEmailChanged(_ context.Context, message service.EmailChangedMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Err != nil {
		return m.Err
	}

	m.notices = append(m.notices, message)

	return nil
}

// Sent is every recovery the transport was asked to deliver, in order.
func (m *Mailer) Sent() []service.RecoveryMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.sent)
}

// Notices is every address change notice it was asked to deliver, in order.
//
// They are kept apart from the recoveries rather than in one list of something
// both satisfy: the two messages have nothing in common but their transport, and
// a test asserting on one should not have to filter out the other.
func (m *Mailer) Notices() []service.EmailChangedMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.notices)
}
