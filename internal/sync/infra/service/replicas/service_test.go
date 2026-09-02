package replicas_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	federationapptest "github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	federationreplica "github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	identityapptest "github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	identityuser "github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/service/replicas"
)

var now = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

type fixture struct {
	adapter *replicas.Service
	origin  *federationserver.Server
	other   *federationserver.Server
	reader  uuid.UUID
}

// newFixture is a replica holding one reader admitted from origin, who has
// also — as the origin's own catalogue would have it — authorized another
// node.
func newFixture(t *testing.T) *fixture {
	t.Helper()

	catalogue := federationapptest.NewServerRepository()
	authorizations := federationapptest.NewReplicaRepository()
	readers := identityapptest.NewUserRepository()

	origin, err := federationserver.New(federationapptest.Descriptor("quire-a.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	other, err := federationserver.New(federationapptest.Descriptor("quire-c.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	for _, node := range []*federationserver.Server{origin, other} {
		if err = catalogue.Create(t.Context(), node); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	localName, err := identityuser.ParseLocalName("guimaraes")
	if err != nil {
		t.Fatalf("ParseLocalName: %v", err)
	}

	displayName, err := identityuser.ParseDisplayName("Guimarães Rosa")
	if err != nil {
		t.Fatalf("ParseDisplayName: %v", err)
	}

	reader := identityuser.Restore(uuid.New(), &identityuser.Props{
		OriginServerID: origin.ID,
		LocalName:      localName,
		DisplayName:    displayName,
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	if err = readers.Create(t.Context(), reader); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, node := range []*federationserver.Server{origin, other} {
		granted, newErr := federationreplica.New(reader.ID, node.ID, false, now)
		if newErr != nil {
			t.Fatalf("replica.New: %v", newErr)
		}

		if err = authorizations.Create(t.Context(), granted); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	return &fixture{
		adapter: replicas.New(catalogue, authorizations, readers),
		origin:  origin,
		other:   other,
		reader:  reader.ID,
	}
}

func TestIdentifyReadsTheCatalogueByPin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	id, err := f.adapter.Identify(t.Context(), f.origin.CertificateFingerprint.String())
	if err != nil || id != f.origin.ID {
		t.Errorf("Identify = %v, %v, want the origin", id, err)
	}

	if _, err = f.adapter.Identify(t.Context(), "sha256:AAAA"); !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("Identify of a pin nobody published = %v, want a permission denied", err)
	}
}

// Replication runs one way. The reader's origin may send their changes here;
// a node the reader also authorized may not, even though it holds a
// permission, because a permission is to hold a copy and not to write under
// the reader's devices.
func TestAuthorizedAdmitsTheOriginAndNobodyElse(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if err := f.adapter.Authorized(t.Context(), f.origin.ID, f.reader); err != nil {
		t.Errorf("Authorized for the origin = %v, want nil", err)
	}

	err := f.adapter.Authorized(t.Context(), f.other.ID, f.reader)
	if !errors.Is(err, errs.KindPermissionDenied) || errs.CodeOf(err) != replicas.CodeNotAuthorized {
		t.Errorf("Authorized for a node that is not the origin = %v, want the same refusal", err)
	}

	err = f.adapter.Authorized(t.Context(), f.origin.ID, uuid.New())
	if !errors.Is(err, errs.KindPermissionDenied) || errs.CodeOf(err) != replicas.CodeNotAuthorized {
		t.Errorf("Authorized for a reader who is not here = %v, want the same refusal", err)
	}
}
