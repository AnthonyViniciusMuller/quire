// Package client_test exercises the half of the client that needs no node: the
// state a device keeps, and the changes it authors while it cannot reach one.
//
// What is not here is every call that goes to the node, and that is deliberate.
// A double of a Quire node would be a second implementation of the reconciler,
// and a client tested against it would be tested against this repository's
// opinion of the contract rather than against the contract; the end-to-end
// suites drive these same methods against a node that really answers.
package client_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/client"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// thisDevice is the appliance the tests author their changes from.
func thisDevice() client.Device {
	return client.Device{
		ID:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		Name:     "the tablet",
		Platform: "cli",
	}
}

// bound writes a state file for a device that has logged in once, and returns
// its path.
//
// It is written as JSON rather than through the client, because that is what a
// device that ran yesterday left behind and it is the form the client has to be
// able to read.
func bound(t *testing.T, state *client.State) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "quirectl.json")

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encoding the state: %v", err)
	}

	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("writing the state: %v", err)
	}

	return path
}

// offline opens a client for a device that cannot reach its node.
func offline(t *testing.T, path string) *client.Client {
	t.Helper()

	opened, err := client.Open(client.Options{StatePath: path, Offline: true})
	if err != nil {
		t.Fatalf("opening the client: %v", err)
	}

	t.Cleanup(func() { _ = opened.Close() })

	return opened
}

// A device that has never been bound cannot author anything, because the
// identifier is what every vector clock entry is keyed by: a change from an
// unbound device is a change no node could attribute to anybody (RN10).
func TestAnUnboundDeviceCannotAuthor(t *testing.T) {
	connection := offline(t, filepath.Join(t.TempDir(), "quirectl.json"))

	_, err := connection.CreateEbook(t.Context(), &client.EbookInput{Title: "Dom Casmurro"})
	if !errors.Is(err, errs.KindFailedPrecondition) {
		t.Fatalf("CreateEbook = %v, want a failed precondition", err)
	}
}

// The disconnected path is a change like any other: it is stamped with this
// device's clock, it names the record it created, and it waits in the log.
func TestAChangeAuthoredOfflineIsQueuedWithItsOwnStamp(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})
	connection := offline(t, path)

	written, err := connection.CreateEbook(t.Context(), &client.EbookInput{
		Title:       "Dom Casmurro",
		Author:      "Machado de Assis",
		Format:      "epub",
		ContentHash: "a1b2",
		Size:        4096,
	})
	if err != nil {
		t.Fatalf("CreateEbook: %v", err)
	}

	if !written.Queued {
		t.Error("the change reported that it reached the node")
	}

	pending := connection.Pending()
	if len(pending) != 1 {
		t.Fatalf("the log holds %d changes, want one", len(pending))
	}

	queued := pending[0]

	if queued.TargetID != written.Target {
		t.Errorf("the queued change names %s and the caller was told %s", queued.TargetID, written.Target)
	}

	if queued.DeviceID != thisDevice().ID {
		t.Errorf("the change was authored by %s", queued.DeviceID)
	}

	if queued.Kind != "insert" || queued.Entity != "ebook" {
		t.Errorf("the change is a %s to a %s", queued.Kind, queued.Entity)
	}

	if counter := queued.VectorClock.Get(crdt.Author(thisDevice().ID)); counter != 1 {
		t.Errorf("the clock counts %d events of this device, want one", counter)
	}

	if queued.CreatedAt.IsZero() {
		t.Error("the change was stamped with no instant")
	}
}

// A second write to the same record ticks over the first, which is what makes
// it causally later rather than concurrent with it. A device that authored two
// changes and did not tick between them would be offering a node two versions
// it cannot order.
func TestASecondChangeToOneRecordTicksOverTheFirst(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})
	connection := offline(t, path)

	written, err := connection.CreateEbook(t.Context(), &client.EbookInput{
		Title: "Dom Casmurro", Format: "epub", ContentHash: "a1b2", Size: 1,
	})
	if err != nil {
		t.Fatalf("CreateEbook: %v", err)
	}

	title := "Dom Casmurro, revisto"
	if _, err = connection.UpdateEbook(t.Context(), written.Target,
		client.EbookChanges{Title: &title}); err != nil {
		t.Fatalf("UpdateEbook: %v", err)
	}

	pending := connection.Pending()
	if len(pending) != 2 {
		t.Fatalf("the log holds %d changes, want two", len(pending))
	}

	author := crdt.Author(thisDevice().ID)
	if counter := pending[1].VectorClock.Get(author); counter != 2 {
		t.Errorf("the second change counts %d events of this device, want two", counter)
	}

	if !pending[1].CreatedAt.After(pending[0].CreatedAt) {
		t.Errorf("the second change was stamped at %s, not after the first at %s",
			pending[1].CreatedAt, pending[0].CreatedAt)
	}

	if fields := len(pending[1].Delta); fields != 1 {
		t.Errorf("the update claims %d fields, want only the one it was given", fields)
	}
}

// An update claims the fields it names and nothing else, which is the whole of
// what per-field reconciliation rests on: what it does not claim stays with
// whichever device wrote it last.
func TestAnUpdateThatClaimsNothingIsRefused(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})
	connection := offline(t, path)

	_, err := connection.UpdateEbook(t.Context(), uuid.New(), client.EbookChanges{})
	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Fatalf("UpdateEbook = %v, want an invalid argument", err)
	}
}

// The filing of a work under a grouping is addressed by the pair and not by the
// row's own identifier (C18), so the delta has to carry both halves — and the
// identifier this device minted for the row has to be the same one next time,
// or one device would fill the log with a new name for one record.
func TestAFilingCarriesThePairAndKeepsItsIdentifier(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})
	connection := offline(t, path)

	work, grouping := uuid.New(), uuid.New()

	filed, err := connection.AddToCollection(t.Context(), work, grouping)
	if err != nil {
		t.Fatalf("AddToCollection: %v", err)
	}

	if !filed.Queued {
		t.Error("the filing reported that it reached the node")
	}

	unfiled, err := connection.RemoveFromCollection(t.Context(), work, grouping)
	if err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}

	pending := connection.Pending()
	if len(pending) != 2 {
		t.Fatalf("the log holds %d changes, want two", len(pending))
	}

	if pending[0].TargetID != pending[1].TargetID {
		t.Errorf("the two changes name %s and %s, which are two records",
			pending[0].TargetID, pending[1].TargetID)
	}

	if unfiled.Target != pending[0].TargetID {
		t.Errorf("the second filing minted %s over %s", unfiled.Target, pending[0].TargetID)
	}

	for _, field := range []string{"ebook_id", "collection_id"} {
		if _, claimed := pending[0].Delta[field]; !claimed {
			t.Errorf("the filing does not claim %s, which is what identifies it", field)
		}
	}

	if pending[1].Kind != "delete" {
		t.Errorf("clearing the register is a %s", pending[1].Kind)
	}
}

// A deletion claims no field, because what it changes is the tombstone, which
// is replication metadata and not a field of the record.
func TestADeletionClaimsNoField(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})
	connection := offline(t, path)

	if _, err := connection.DeleteAnnotation(t.Context(), uuid.New()); err != nil {
		t.Fatalf("DeleteAnnotation: %v", err)
	}

	queued := connection.Pending()[0]
	if len(queued.Delta) != 0 {
		t.Errorf("the deletion claims %v", queued.Delta)
	}

	if queued.Kind != "delete" || queued.Entity != "annotation" {
		t.Errorf("the change is a %s to a %s", queued.Kind, queued.Entity)
	}
}

// A change authored offline outlives the process that authored it: the whole
// point of the log is that it is handed over by a later run.
func TestTheLogSurvivesTheProcessThatWroteIt(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})

	first := offline(t, path)

	written, err := first.CreateCollection(t.Context(),
		&client.CollectionInput{Name: "Modernismo", Kind: "collection"})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	second := offline(t, path)

	pending := second.Pending()
	if len(pending) != 1 {
		t.Fatalf("the reopened client holds %d changes, want one", len(pending))
	}

	if pending[0].TargetID != written.Target {
		t.Errorf("the reopened client holds a change to %s, want %s", pending[0].TargetID, written.Target)
	}
}

// The clock is what the state exists to carry across runs. A device that
// restarted having observed nothing would stamp its next change from its wall
// clock, which on a machine running behind is under what it has already
// stamped — the cycle C01 exists to remove.
func TestTheClockIsCarriedAcrossRuns(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})

	first := offline(t, path)
	if _, err := connectionWrite(t, first); err != nil {
		t.Fatalf("the first change: %v", err)
	}

	stamped := first.Pending()[0].CreatedAt

	second := offline(t, path)
	if _, err := connectionWrite(t, second); err != nil {
		t.Fatalf("the second change: %v", err)
	}

	if observed := second.State().ObservedAt; observed.Before(stamped) {
		t.Errorf("the reopened client observed %s, which is before %s", observed, stamped)
	}

	if next := second.Pending()[1].CreatedAt; !next.After(stamped) {
		t.Errorf("the reopened client stamped %s, which is not after %s", next, stamped)
	}
}

// connectionWrite authors one change, which these tests need only as an event.
func connectionWrite(t *testing.T, connection *client.Client) (client.Written, error) {
	t.Helper()

	return connection.CreateCollection(t.Context(),
		&client.CollectionInput{Name: "Modernismo", Kind: "collection"})
}

// Everything the node alone can answer is refused while the client is offline,
// and the refusal says which call it was rather than failing at a dial.
func TestReadsAreRefusedWhileOffline(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})
	connection := offline(t, path)

	if _, err := connection.ListDevices(t.Context(), false); !errors.Is(err, errs.KindFailedPrecondition) {
		t.Errorf("ListDevices = %v, want a failed precondition", err)
	}

	if _, err := connection.Whoami(t.Context()); !errors.Is(err, errs.KindFailedPrecondition) {
		t.Errorf("Whoami = %v, want a failed precondition", err)
	}
}

// The state holds a refresh credential, which is the one secret a device
// carries that is worth stealing, so the file it is kept in is readable by its
// owner alone.
func TestTheStateIsWrittenForItsOwnerAlone(t *testing.T) {
	path := bound(t, &client.State{Device: thisDevice()})

	if _, err := connectionWrite(t, offline(t, path)); err != nil {
		t.Fatalf("the change: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the state: %v", err)
	}

	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the state is kept as %o, want 600", mode)
	}
}

// A state file that is not one is reported as such rather than being read as an
// empty device, which would silently start a second clock entry.
func TestAStateFileThatIsNotOneIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quirectl.json")

	if err := os.WriteFile(path, []byte("not a device"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	if _, err := client.Open(client.Options{StatePath: path, Offline: true}); err == nil {
		t.Fatal("the client read a state file that is not one")
	}
}
