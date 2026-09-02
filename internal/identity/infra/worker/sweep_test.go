package worker_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/worker"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

var now = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

// A sweep removes what has expired and leaves what has not, consumed or
// not: a spent credential that is still within its lifetime is what reuse
// detection reads (D07), and only its expiry makes it safe to forget.
func TestOnceRemovesOnlyWhatHasExpired(t *testing.T) {
	t.Parallel()

	credentials := apptest.NewCredentialRepository()
	reader, phone := uuid.New(), uuid.New()

	expired, err := credential.NewSession(reader, phone, "spent and old", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	live, err := credential.NewSession(reader, phone, "current", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	for _, stored := range []*credential.Credential{expired, live} {
		if err = credentials.Create(t.Context(), stored); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	sweep := worker.New(credentials, apptest.NewClock(now), logging.Discard())

	removed, err := sweep.Once(t.Context())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}

	if removed != 1 {
		t.Errorf("the sweep removed %d credentials, want the one that expired", removed)
	}

	if credentials.Live(credential.KindSessionRefresh, now) != 1 {
		t.Error("the sweep removed a credential that is still within its lifetime")
	}
}
