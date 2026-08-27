//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"
	"uuid"

	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	credentialrepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/credential"
	devicerepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/device"
	userrepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/user"
	localserverservice "github.com/anthonyvsmuller/quire/internal/identity/infra/service/localserver"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// repositories is the set under test, over one transaction manager.
type repositories struct {
	manager     *persist.Manager
	users       *userrepository.Repository
	devices     *devicerepository.Repository
	credentials *credentialrepository.Repository
	serverID    uuid.UUID
}

// newRepositories resets the database and returns the repositories with this
// node's catalogue row already created.
func newRepositories(t *testing.T) repositories {
	t.Helper()
	reset(t)

	cfg := nodeConfig(t)
	manager := persist.NewManager(pool)

	serverID, err := localServer(t, manager, cfg).ID(t.Context())
	if err != nil {
		t.Fatalf("resolving this node's catalogue row: %v", err)
	}

	return repositories{
		manager:     manager,
		users:       userrepository.New(manager),
		devices:     devicerepository.New(manager),
		credentials: credentialrepository.New(manager),
		serverID:    serverID,
	}
}

// localServer is the resolver of UC14, over the federation slice's catalogue.
func localServer(t *testing.T, manager *persist.Manager, cfg *config.Config) *localserverservice.Service {
	t.Helper()

	resolver, err := localserverservice.New(serverrepository.New(manager), &cfg.Server)
	if err != nil {
		t.Fatalf("building the local server resolver: %v", err)
	}

	return resolver
}

// reader is a registered reader, written.
func (r repositories) reader(t *testing.T, localName user.LocalName, email user.Email) *user.User {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Microsecond)

	record, err := user.New(&user.Props{
		OriginServerID: r.serverID,
		LocalName:      localName,
		DisplayName:    "Anthony",
		Email:          email,
		PasswordHash:   "$2a$04$digest",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("user.New: %v", err)
	}

	if err := r.users.Create(t.Context(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return record
}

// TestLocalServerIsIdempotent covers the bootstrap every registration depends
// on: the row is created once and found afterwards, and a second node process
// starting against the same database does not mint a second identity.
func TestLocalServerIsIdempotent(t *testing.T) {
	reset(t)

	cfg := nodeConfig(t)
	manager := persist.NewManager(pool)

	first, err := localServer(t, manager, cfg).ID(t.Context())
	if err != nil {
		t.Fatalf("the first resolution: %v", err)
	}

	// A different instance of the resolver, as a restarted process would build.
	second, err := localServer(t, manager, cfg).ID(t.Context())
	if err != nil {
		t.Fatalf("the second resolution: %v", err)
	}

	if first != second {
		t.Errorf("the node has two identities, %s and %s", first, second)
	}

	var rows int
	if err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM federation.servers WHERE is_local").Scan(&rows); err != nil {
		t.Fatalf("counting the local rows: %v", err)
	}

	if rows != 1 {
		t.Errorf("%d rows claim to be this node, want exactly 1", rows)
	}
}

// TestUniquenessOfRN09 is what the doubles imitate, checked against the indexes
// that actually decide it.
func TestUniquenessOfRN09(t *testing.T) {
	r := newRepositories(t)
	r.reader(t, "anthony", "anthony@example.test")

	tests := []struct {
		name      string
		localName user.LocalName
		email     user.Email
		code      string
	}{
		{name: "the same local name", localName: "anthony", email: "other@example.test", code: user.CodeLocalNameTaken},
		{
			// The index is over lower(email), so a different capitalization is
			// the same address.
			name: "the same address in another capitalization", localName: "somebody",
			email: "ANTHONY@Example.test", code: user.CodeEmailRegistered,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Microsecond)

			record, err := user.New(&user.Props{
				OriginServerID: r.serverID,
				LocalName:      test.localName,
				DisplayName:    "Somebody",
				Email:          test.email,
				PasswordHash:   "$2a$04$digest",
				CreatedAt:      now,
				UpdatedAt:      now,
			})
			if err != nil {
				t.Fatalf("user.New: %v", err)
			}

			err = r.users.Create(t.Context(), record)
			if err == nil {
				t.Fatal("the database accepted a duplicate")
			}

			if !errs.Retryable(err) && !isAlreadyExists(err) {
				t.Errorf("error = %v, want already exists", err)
			}

			if code := errs.CodeOf(err); code != test.code {
				t.Errorf("code = %q, want %q — the constraint name is what tells the two apart", code, test.code)
			}
		})
	}
}

// isAlreadyExists is the kind a uniqueness failure has to reach the transport
// as.
func isAlreadyExists(err error) bool { return errs.KindOf(err) == errs.KindAlreadyExists }

// TestTheSameNameOnAnotherServer is the rest of RN09: the identifier is unique
// on the pair, so the same local name under another origin server is another
// reader.
func TestTheSameNameOnAnotherServer(t *testing.T) {
	r := newRepositories(t)
	r.reader(t, "anthony", "anthony@example.test")

	var peerID uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO federation.servers (domain, base_url) VALUES ($1, $2) RETURNING id`,
		"quire-b.example", "https://quire-b.example").Scan(&peerID); err != nil {
		t.Fatalf("adding a peer to the catalogue: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)

	record, err := user.New(&user.Props{
		OriginServerID: peerID,
		LocalName:      "anthony",
		DisplayName:    "A different Anthony",
		Email:          "anthony@example.test",
		PasswordHash:   "$2a$04$digest",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("user.New: %v", err)
	}

	if err := r.users.Create(t.Context(), record); err != nil {
		t.Errorf("the same name on another server was refused: %v", err)
	}
}

// TestGetByEmailFoldsCase covers the statement rather than the index: a lookup
// that compared the stored capitalization would report an address free right up
// to the insert that fails.
func TestGetByEmailFoldsCase(t *testing.T) {
	r := newRepositories(t)
	stored := r.reader(t, "anthony", "Anthony@Example.test")

	found, err := r.users.GetByEmail(t.Context(), r.serverID, "anthony@example.test")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}

	if found.ID != stored.ID {
		t.Error("the lookup found another reader")
	}

	// And the stored value keeps the reader's own capitalization.
	if found.Email != "Anthony@Example.test" {
		t.Errorf("Email = %q, want it as the reader typed it", found.Email)
	}
}

// TestConsumeIsAtomic is the one property a single-threaded test cannot see, and
// the reason the statement carries NOT consumed in its where clause: two devices
// presenting the same credential at the same instant must not both be answered
// with a session.
func TestConsumeIsAtomic(t *testing.T) {
	r := newRepositories(t)
	reader := r.reader(t, "anthony", "anthony@example.test")

	appliance, err := device.New(reader.ID, "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	err = r.devices.Create(t.Context(), appliance)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	issued, err := credential.NewSession(reader.ID, appliance.ID, "digest", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := r.credentials.Create(t.Context(), issued); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const racers = 8

	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		succeeded int
	)

	wait.Add(racers)

	for range racers {
		go func() {
			defer wait.Done()

			if err := r.credentials.Consume(t.Context(), issued.ID); err == nil {
				mutex.Lock()
				succeeded++
				mutex.Unlock()
			}
		}()
	}

	wait.Wait()

	if succeeded != 1 {
		t.Errorf("%d of %d callers spent the same credential, want exactly 1", succeeded, racers)
	}
}

// TestListDevicesByUser covers the ordering and the flag, both of which live in
// the statement.
func TestListDevicesByUser(t *testing.T) {
	r := newRepositories(t)
	reader := r.reader(t, "anthony", "anthony@example.test")

	for _, name := range []device.Name{"Tablet", "Pixel 9", "An old phone"} {
		appliance, err := device.New(reader.ID, name, "android")
		if err != nil {
			t.Fatalf("device.New: %v", err)
		}

		if name == "An old phone" {
			appliance.Revoke()
		}

		if err := r.devices.Create(t.Context(), appliance); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	bound, err := r.devices.ListByUser(t.Context(), reader.ID, false)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}

	if len(bound) != 2 {
		t.Fatalf("%d devices came back, want the two bound ones", len(bound))
	}

	if bound[0].Name != "Pixel 9" || bound[1].Name != "Tablet" {
		t.Errorf("the devices came back as %q then %q, want them ordered by name",
			bound[0].Name, bound[1].Name)
	}

	all, err := r.devices.ListByUser(t.Context(), reader.ID, true)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}

	if len(all) != 3 {
		t.Errorf("%d devices came back when the unbound were asked for, want 3", len(all))
	}
}

// TestWithinRollsBackEverything is what one unit of work has to mean: a login
// that bound a device and then failed to store its credential must leave no
// device behind.
func TestWithinRollsBackEverything(t *testing.T) {
	r := newRepositories(t)
	reader := r.reader(t, "anthony", "anthony@example.test")

	appliance, err := device.New(reader.ID, "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	failure := errs.New(errs.KindInternal, "the work failed after the device was bound")

	err = r.manager.Within(t.Context(), func(ctx context.Context) error {
		if createErr := r.devices.Create(ctx, appliance); createErr != nil {
			return createErr
		}

		return failure
	})
	if err == nil {
		t.Fatal("Within reported success for work that failed")
	}

	found, err := r.devices.ListByUser(t.Context(), reader.ID, true)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}

	if len(found) != 0 {
		t.Errorf("%d devices survived a rolled back unit of work", len(found))
	}
}

// TestDeletingAReaderCascades is what the delete use case relies on instead of
// removing anything by hand: what belongs to a reader is decided by the foreign
// keys.
func TestDeletingAReaderCascades(t *testing.T) {
	r := newRepositories(t)
	reader := r.reader(t, "anthony", "anthony@example.test")

	appliance, err := device.New(reader.ID, "Pixel 9", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	err = r.devices.Create(t.Context(), appliance)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	issued, err := credential.NewSession(reader.ID, appliance.ID, "digest", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := r.credentials.Create(t.Context(), issued); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := r.users.Delete(t.Context(), reader.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, table := range []string{"identity.devices", "identity.credentials"} {
		var rows int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&rows); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}

		if rows != 0 {
			t.Errorf("%d rows survive in %s, so deleting a reader leaves them behind", rows, table)
		}
	}
}
