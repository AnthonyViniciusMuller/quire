package replicateoperations_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/replicateoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// authored is when the changes below were made.
var authored = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// pin is what the calling node's discovery document publishes.
const pin = "spki-sha256:Ym9tIGRpYSwgc2VydGFvIGRlIFZpZGFzIFNlY2Fz"

// fixture is the peer-facing use case over the ingest it delegates to.
type fixture struct {
	usecase  *replicateoperations.ReplicateOperations
	replicas *apptest.Replicas
	log      *apptest.OperationRepository

	peer   uuid.UUID
	reader uuid.UUID
	phone  uuid.UUID
}

func newFixture() *fixture {
	log := apptest.NewOperationRepository()
	replicas := apptest.NewReplicas()

	ingest := pushoperations.New(log, apptest.NewRecords(), apptest.NewClock(authored),
		apptest.NewTransaction(log), apptest.NewChanges())

	f := &fixture{
		usecase:  replicateoperations.New(replicas, ingest),
		replicas: replicas,
		log:      log,
		peer:     uuid.New(),
		reader:   uuid.New(),
		phone:    uuid.New(),
	}

	f.replicas.Know(pin, f.peer)
	f.replicas.Authorize(f.peer, f.reader)

	return f
}

// change is a well-formed change authored by device.
func change(device uuid.UUID, counter uint64) pushoperations.Change {
	return pushoperations.Change{
		ID:           uuid.New(),
		DeviceID:     device,
		TargetEntity: "ebook",
		TargetID:     uuid.New(),
		Kind:         "update",
		Delta:        operation.Delta{"title": json.RawMessage(`"Vidas Secas"`)},
		VectorClock:  crdt.VectorClock{crdt.Author(device): counter},
		CreatedAt:    authored,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), replicateoperations.Input{
		Pin:        pin,
		UserID:     f.reader,
		Operations: []pushoperations.Change{change(f.phone, 1)},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Results) != 1 || output.Results[0].Outcome != operation.OutcomeApplied {
		t.Fatalf("the peer was answered %+v, want one applied", output.Results)
	}

	if f.log.Len() != 1 {
		t.Errorf("the log holds %d changes after the peer's batch", f.log.Len())
	}
}

// A peer replicates many devices and is none of them, so there is no author for
// a batch to declare — and RN10, which fails a device's batch whole, has
// nothing to check here.
func TestExecuteAcceptsChangesAuthoredByAnyDevice(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), replicateoperations.Input{
		Pin:        pin,
		UserID:     f.reader,
		Operations: []pushoperations.Change{change(uuid.New(), 1), change(uuid.New(), 1)},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for index, result := range output.Results {
		if result.Outcome != operation.OutcomeApplied {
			t.Errorf("change %d = %s, want applied", index, result.Outcome)
		}
	}
}

// A caller the catalogue does not hold is refused before it learns anything
// about the reader it named.
func TestExecuteRefusesACallerThisNodeDoesNotKnow(t *testing.T) {
	t.Parallel()

	f := newFixture()

	_, err := f.usecase.Execute(t.Context(), replicateoperations.Input{
		Pin:        "spki-sha256:c29tZWJvZHkgZWxzZQ==",
		UserID:     f.reader,
		Operations: []pushoperations.Change{change(f.phone, 1)},
	})
	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Fatalf("Execute = %v, want a permission denied", err)
	}

	if f.log.Len() != 0 {
		t.Error("a caller this node does not know had its changes stored anyway")
	}
}

// This is the only call in the contract refused on a reader's own instruction,
// and RN03 is why. A reader who never authorized the node and one who revoked
// it are the same answer.
func TestExecuteRefusesAReaderWhoHasNotAuthorizedTheCaller(t *testing.T) {
	t.Parallel()

	f := newFixture()

	_, err := f.usecase.Execute(t.Context(), replicateoperations.Input{
		Pin:        pin,
		UserID:     uuid.New(),
		Operations: []pushoperations.Change{change(f.phone, 1)},
	})
	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Fatalf("Execute = %v, want a permission denied", err)
	}

	if f.log.Len() != 0 {
		t.Error("a reader who authorized nobody had a peer's changes stored under their name")
	}
}
