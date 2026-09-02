package admissions_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	federationservice "github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/service/admissions"
)

var now = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

type fixture struct {
	adapter *admissions.Service
	readers *apptest.Readers
	peers   *apptest.Peers
	node    uuid.UUID
	reader  federationservice.Reader
	phone   federationservice.Device
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	readers := apptest.NewReaders()
	peers := apptest.NewPeers()
	authorizations := apptest.NewReplicaRepository()

	f := &fixture{
		adapter: admissions.New(readers, peers, authorizations),
		readers: readers,
		peers:   peers,
		node:    uuid.New(),
		reader:  federationservice.Reader{ID: uuid.New(), LocalName: "guimaraes", DisplayName: "Guimarães Rosa"},
		phone:   federationservice.Device{ID: uuid.New(), Name: "the phone", Platform: "android"},
	}

	readers.Bind(f.reader, f.phone)

	granted, err := replica.New(f.reader.ID, f.node, true, now)
	if err != nil {
		t.Fatalf("replica.New: %v", err)
	}

	if err = authorizations.Create(t.Context(), granted); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return f
}

func TestAdmitTellsTheNodeWhoTheReaderIs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if err := f.adapter.Admit(t.Context(), f.node, f.reader.ID); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	told := f.peers.Admissions(f.node)
	if len(told) != 1 {
		t.Fatalf("the node was told %d times, want once", len(told))
	}

	switch {
	case told[0].Reader != f.reader:
		t.Errorf("the node was told the reader is %+v", told[0].Reader)
	case len(told[0].Devices) != 1 || told[0].Devices[0] != f.phone:
		t.Errorf("the node was told the devices are %+v", told[0].Devices)
	case !told[0].ReplicatesFiles:
		t.Error("the node was not told the permission covers the files")
	}
}

// The node is told again only when there is something new to tell: a device
// bound since, or a call that did not land.
func TestAdmitRepeatsItselfOnlyWhenSomethingChanged(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for range 3 {
		if err := f.adapter.Admit(t.Context(), f.node, f.reader.ID); err != nil {
			t.Fatalf("Admit: %v", err)
		}
	}

	if told := f.peers.Admissions(f.node); len(told) != 1 {
		t.Errorf("the node was told %d times about the same thing, want once", len(told))
	}

	tablet := federationservice.Device{ID: uuid.New(), Name: "the tablet", Platform: "ios"}
	f.readers.Bind(f.reader, f.phone, tablet)

	if err := f.adapter.Admit(t.Context(), f.node, f.reader.ID); err != nil {
		t.Fatalf("Admit after a binding: %v", err)
	}

	told := f.peers.Admissions(f.node)
	if len(told) != 2 || len(told[1].Devices) != 2 {
		t.Fatalf("the node was told %v after a device was bound, want the new device carried", told)
	}
}

func TestAdmitTriesAgainAfterANodeThatDidNotAnswer(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.peers.Err = errs.New(errs.KindUnavailable, "no route to host")

	if err := f.adapter.Admit(t.Context(), f.node, f.reader.ID); !errors.Is(err, errs.KindUnavailable) {
		t.Fatalf("Admit = %v, want the node's silence reported", err)
	}

	f.peers.Err = nil

	if err := f.adapter.Admit(t.Context(), f.node, f.reader.ID); err != nil {
		t.Fatalf("Admit once the node answers: %v", err)
	}

	if told := f.peers.Admissions(f.node); len(told) != 1 {
		t.Errorf("the node was told %d times, want the one call that landed", len(told))
	}
}

func TestAdmitReportsAReaderThisNodeDoesNotHold(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if err := f.adapter.Admit(t.Context(), f.node, uuid.New()); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("Admit = %v, want a not found", err)
	}
}
