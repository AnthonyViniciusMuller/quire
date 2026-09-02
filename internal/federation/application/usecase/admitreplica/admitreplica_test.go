package admitreplica_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/admitreplica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

var now = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

type fixture struct {
	usecase     *admitreplica.AdmitReplica
	servers     *apptest.ServerRepository
	replicas    *apptest.ReplicaRepository
	readers     *apptest.Readers
	transaction *apptest.Transaction
	origin      *server.Server
	reader      service.Reader
	phone       service.Device
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	servers := apptest.NewServerRepository()
	replicas := apptest.NewReplicaRepository()
	readers := apptest.NewReaders()
	transaction := apptest.NewTransaction()

	origin, err := server.New(apptest.Descriptor("quire-a.example"), now)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	if err = servers.Create(t.Context(), origin); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase:     admitreplica.New(servers, replicas, readers, apptest.NewClock(now), transaction),
		servers:     servers,
		replicas:    replicas,
		readers:     readers,
		transaction: transaction,
		origin:      origin,
		reader:      service.Reader{ID: uuid.New(), LocalName: "guimaraes", DisplayName: "Guimarães Rosa"},
		phone:       service.Device{ID: uuid.New(), Name: "the phone", Platform: "android"},
	}
}

// input is the call the origin makes, said with the origin's own pin.
func (f *fixture) input() admitreplica.Input {
	return admitreplica.Input{
		Pin:             f.origin.CertificateFingerprint.String(),
		Reader:          f.reader,
		Devices:         []service.Device{f.phone},
		ReplicatesFiles: true,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if origin, held := f.readers.OriginOf(f.reader.ID); !held || origin != f.origin.ID {
		t.Error("the reader was not recorded as the origin's")
	}

	if !f.readers.Holds(f.reader.ID, f.phone.ID) {
		t.Error("the device was not recorded under the reader")
	}

	granted, err := f.replicas.GetByPair(t.Context(), f.reader.ID, f.origin.ID)
	if err != nil {
		t.Fatalf("the permission was not recorded: %v", err)
	}

	switch {
	case !granted.Active:
		t.Error("the permission does not stand")
	case !granted.ReplicatesFiles:
		t.Error("the files were left out of a permission that covered them")
	case !granted.AuthorizedAt.Equal(now):
		t.Error("the row does not say when this node was told")
	}

	if f.transaction.Calls() != 1 {
		t.Errorf("units of work = %d, want the reader and the permission in one", f.transaction.Calls())
	}
}

// Admission is a standing obligation and not a handshake: the second call
// changes nothing but what it adds.
func TestExecuteAgainReusesTheRow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("the first admission: %v", err)
	}

	first, err := f.replicas.GetByPair(t.Context(), f.reader.ID, f.origin.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	again := f.input()
	again.ReplicatesFiles = false

	if _, err = f.usecase.Execute(t.Context(), again); err != nil {
		t.Fatalf("the second admission: %v", err)
	}

	second, err := f.replicas.GetByPair(t.Context(), f.reader.ID, f.origin.ID)
	if err != nil {
		t.Fatalf("GetByPair: %v", err)
	}

	if second.ID != first.ID {
		t.Error("the second admission wrote a second row, and the pair now has two histories")
	}

	if second.ReplicatesFiles {
		t.Error("the permission was not narrowed to what the origin now states")
	}

	if f.readers.Admitted() != 2 {
		t.Errorf("the reader was admitted %d times, want the identity slice told both times", f.readers.Admitted())
	}
}

// The caller is its certificate. A pin the catalogue does not name, a node
// the operator has stopped, and no pin at all are refused the same way, so
// that a peer cannot tell whether it was ever in the catalogue.
func TestExecuteRefusesACallerTheCatalogueDoesNotName(t *testing.T) {
	t.Parallel()

	stopped := func(t *testing.T, f *fixture) {
		t.Helper()

		f.origin.Active = false

		if err := f.servers.Update(t.Context(), f.origin); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	tests := map[string]struct {
		pin    func(*fixture) string
		breaks func(*testing.T, *fixture)
	}{
		"no certificate":          {pin: func(*fixture) string { return "" }},
		"a pin nobody published":  {pin: func(*fixture) string { return "sha256:AAAA" }},
		"a node that was stopped": {pin: func(f *fixture) string { return f.input().Pin }, breaks: stopped},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			input := f.input()
			input.Pin = test.pin(f)

			if test.breaks != nil {
				test.breaks(t, f)
			}

			_, err := f.usecase.Execute(t.Context(), input)
			if !errors.Is(err, errs.KindPermissionDenied) || errs.CodeOf(err) != admitreplica.CodeUnknownPeer {
				t.Errorf("Execute = %v, want the caller refused as unknown", err)
			}

			if f.readers.Admitted() != 0 {
				t.Error("a caller the catalogue does not name admitted a reader")
			}
		})
	}
}

// A reader this node holds under another node is not the caller's to claim,
// and the refusal leaves no permission behind.
func TestExecuteRefusesAReaderHeldUnderAnotherNode(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.readers.Host(f.reader.ID, uuid.New())

	_, err := f.usecase.Execute(t.Context(), f.input())
	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("Execute = %v, want a permission denied", err)
	}

	if _, err = f.replicas.GetByPair(t.Context(), f.reader.ID, f.origin.ID); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("a permission was recorded for a reader the caller may not claim: %v", err)
	}
}
