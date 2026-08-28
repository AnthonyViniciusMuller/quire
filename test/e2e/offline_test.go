//go:build e2e

package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/client"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// UC11, in the shape a reader would recognize: a device writes on a train and
// the reader's other device has it that evening.
//
// Nothing about the change says it was made offline. It is stamped by the
// device that made it, pushed when there is a network, and read back through
// the ordinary call — which is the contract's requirement that the connected
// and disconnected paths be indistinguishable once applied, checked from the
// far end of both.
func TestAChangeMadeOfflineReachesTheOtherDevice(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")
	phone := newDevice(t, nodeA, who, "phone")

	tablet.disconnect(t)

	written, err := tablet.CreateEbook(t.Context(), &client.EbookInput{
		Title:       "Dom Casmurro",
		Author:      "Machado de Assis",
		Format:      "epub",
		ContentHash: digestOf(t, "dom-casmurro"),
		Size:        4096,
	})
	if err != nil {
		t.Fatalf("the tablet writing offline: %v", err)
	}

	if !written.Queued {
		t.Fatal("the tablet reported reaching the node while it was disconnected")
	}

	tablet.reconnect(t)
	push(t, tablet)

	if held := title(t, phone, written.Target); held != "Dom Casmurro" {
		t.Errorf("the phone reads the work as %q", held)
	}

	if pulled := drain(t, phone); !holds(pulled, written.Target) {
		t.Errorf("the phone pulled %d changes and none of them is the work", len(pulled))
	}
}

// RNF03 and C01, which is the claim the whole design rests on: two devices that
// wrote the same field while neither could see the other converge on one
// answer, and the answer does not depend on the order the writes arrived in.
//
// The test makes the order wrong on purpose. The tablet writes first and the
// phone second, so the phone's write is the later of the two on the clock C01
// describes; then the phone pushes first and the tablet second. If the node
// took the last write it received, the tablet would win — and what it takes is
// the greater of the two on (updated_at, device_id), so the phone does.
func TestConcurrentOfflineEditsConvergeWhicheverArrivesFirst(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")
	phone := newDevice(t, nodeA, who, "phone")

	work := createWork(t, tablet, "Memórias Póstumas")

	// The phone reads it, which is how a device that did not write a record
	// learns the version its own next change has to be stamped on top of.
	if held := title(t, phone, work); held != "Memórias Póstumas" {
		t.Fatalf("the phone reads the work as %q before anybody edited it", held)
	}

	tablet.disconnect(t)
	phone.disconnect(t)

	edit(t, tablet, work, "Memórias Póstumas de Brás Cubas")
	edit(t, phone, work, "Memórias Póstumas, edição do centenário")

	// Captured before the push, because a change the node has answered leaves
	// the log — and these two stamps are what the tie-break is over.
	tabletEdit, phoneEdit := onlyPending(t, tablet), onlyPending(t, phone)

	winner, loser := tablet, phone
	winning, losing := "Memórias Póstumas de Brás Cubas", "Memórias Póstumas, edição do centenário"

	if beats(&phoneEdit, &tabletEdit) {
		winner, loser = phone, tablet
		winning, losing = losing, winning
	}

	phone.reconnect(t)
	tablet.reconnect(t)

	// The loser pushes whenever it pushes. What is being checked is that this
	// makes no difference at all.
	first, second := phone, tablet
	if winner == phone {
		first, second = tablet, phone
	}

	push(t, first)

	late := push(t, second)
	if len(late.Results) != 1 {
		t.Fatalf("the second push offered %d changes", len(late.Results))
	}

	expected := quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED
	if second == loser {
		expected = quirev1.OperationOutcome_OPERATION_OUTCOME_SUPERSEDED
	}

	if outcome := late.Results[0].GetOutcome(); outcome != expected {
		t.Errorf("the second push was answered %s, want %s", outcome, expected)
	}

	for _, appliance := range []*device{tablet, phone} {
		if held := title(t, appliance, work); held != winning {
			t.Errorf("%s reads the work as %q, want %q", appliance.name, held, winning)
		}
	}

	t.Logf("%s won with %q; %s wrote %q and lost", winner.name, winning, loser.name, losing)

	// Both edits are in the log, the losing one included: a node that had
	// dropped it would hold a history from which a later node could not reach
	// the same conclusion. Two and not three, because the work was created
	// through the connected path and a change made that way appends nothing —
	// C21 in docs/tcc-corrections.md, which TestAChangeMadeWhileConnectedReachesNoLog
	// pins on its own.
	if pulled := drain(t, tablet); len(pulled) != 2 {
		t.Errorf("the log holds %d changes, want both edits", len(pulled))
	}
}

// A deletion is a write like any other, and this is the half of that which only
// two devices can show: the work is gone for the device that did not delete it,
// because what travelled is a tombstone and not a removal.
func TestADeletionMadeOfflineTravels(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")
	phone := newDevice(t, nodeA, who, "phone")

	work := createWork(t, tablet, "Quincas Borba")

	if held := title(t, phone, work); held != "Quincas Borba" {
		t.Fatalf("the phone reads the work as %q before it was deleted", held)
	}

	tablet.disconnect(t)

	if _, err := tablet.DeleteEbook(t.Context(), work); err != nil {
		t.Fatalf("the tablet deleting offline: %v", err)
	}

	tablet.reconnect(t)
	push(t, tablet)

	if _, err := phone.GetEbook(t.Context(), work); !gone(err) {
		t.Errorf("the phone still reads the work: %v", err)
	}
}

// UC10 without a poll: a device that stays connected is handed what another
// device wrote, as it is written.
//
// The stream is the one part of the contract a request and a reply cannot show,
// and the acknowledgement is what keeps it flowing — the node sends one page
// and waits to be told it landed.
func TestAChangeReachesAWatchingDeviceAsItHappens(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")
	phone := newDevice(t, nodeA, who, "phone")

	watching, stop := watch(t, tablet)
	defer stop()

	phone.disconnect(t)

	written, err := phone.CreateEbook(t.Context(), &client.EbookInput{
		Title:       "Esaú e Jacó",
		Format:      "epub",
		ContentHash: digestOf(t, "esau-e-jaco"),
		Size:        2048,
	})
	if err != nil {
		t.Fatalf("the phone writing offline: %v", err)
	}

	phone.reconnect(t)
	push(t, phone)

	deadline := time.After(settleFor)

	for {
		select {
		case operation := <-watching:
			if operation.GetTargetId() == written.Target.String() {
				return
			}
		case <-deadline:
			t.Fatalf("the tablet watched for %s and was not told about the work", settleFor)
		}
	}
}

// The cursor is what a device brings back after being away, and it is in the
// file rather than in the process: a device that pulled yesterday and was
// restarted is not a device that has to read the whole log again.
func TestTheCursorSurvivesTheDeviceBeingRestarted(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")

	// Written offline and pushed, because that is what puts anything in the
	// log at all: a work created through the connected path appends nothing
	// (C21), and a device draining an empty log would keep the cursor it
	// started with and prove nothing.
	tablet.disconnect(t)
	createWork(t, tablet, "Helena")
	tablet.reconnect(t)
	push(t, tablet)

	drain(t, tablet)

	taken := tablet.Cursor()
	if taken == 0 {
		t.Fatal("the tablet drained the log and kept no cursor")
	}

	// The same device, started again over the same file. Nothing else is
	// carried across, which is the point of checking it this way.
	restarted := open(t, nodeA, tablet.statePath, false)

	if kept := restarted.Cursor(); kept != taken {
		t.Errorf("the restarted device holds cursor %d, want %d", kept, taken)
	}

	report, err := restarted.Pull(t.Context(), 0)
	if err != nil {
		t.Fatalf("the restarted device pulling: %v", err)
	}

	if len(report.Operations) != 0 {
		t.Errorf("the restarted device was handed %d changes it had already seen",
			len(report.Operations))
	}
}

// C21 in docs/tcc-corrections.md, observed from outside the node.
//
// A change made through the API while a device is online produces no operation,
// so it reaches neither the reader's other devices nor an authorized replica,
// while the same change made offline reaches both. Phase 9 found it by reading
// the code that writes the log; this is what it looks like from a device: the
// work is there, every call reports it, and the log this reader's devices
// synchronize through has never heard of it.
//
// The test pins the finding rather than the fix. When the outbox C21 describes
// lands in the write use cases of the library and reading slices, this is the
// test that fails, and it should: what it says is only that the gap is still
// there and still exactly this size.
func TestAChangeMadeWhileConnectedReachesNoLog(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")
	phone := newDevice(t, nodeA, who, "phone")

	work := createWork(t, tablet, "Iracema")

	// The work exists, and the connected path is how the phone can tell.
	if held := title(t, phone, work); held != "Iracema" {
		t.Fatalf("the phone reads the work as %q", held)
	}

	if pulled := drain(t, phone); holds(pulled, work) {
		t.Fatal("a change made through the connected path reached the log, " +
			"which is C21 being fixed: this test is the one to update")
	}
}

// createWork registers a work through the connected path and returns it.
func createWork(t *testing.T, appliance *device, name string) uuid.UUID {
	t.Helper()

	written, err := appliance.CreateEbook(t.Context(), &client.EbookInput{
		Title:       name,
		Format:      "epub",
		ContentHash: digestOf(t, name),
		Size:        1024,
	})
	if err != nil {
		t.Fatalf("%s registering %q: %v", appliance.name, name, err)
	}

	return written.Target
}

// edit renames a work, whichever path the device is currently on.
func edit(t *testing.T, appliance *device, work uuid.UUID, name string) {
	t.Helper()

	if _, err := appliance.UpdateEbook(t.Context(), work,
		client.EbookChanges{Title: &name}); err != nil {
		t.Fatalf("%s renaming the work to %q: %v", appliance.name, name, err)
	}
}

// onlyPending is the single change a device is holding, and a failure when it
// is holding any other number: a test that meant to make one change and made
// two would otherwise compare the wrong stamps.
func onlyPending(t *testing.T, appliance *device) client.Operation {
	t.Helper()

	pending := appliance.Pending()
	if len(pending) != 1 {
		t.Fatalf("%s holds %d changes, want one", appliance.name, len(pending))
	}

	return pending[0]
}

// beats is the tie-break of C01, applied to two changes this test made
// concurrently: the greater instant wins, and the device identifier settles the
// case where two devices landed on the same one.
//
// It is written out here rather than taken from internal/shared/crdt on
// purpose. What the test asserts is that the node applies this rule, and a test
// that asked the node's own code which write should win would agree with it
// whatever it did.
func beats(left, right *client.Operation) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}

	return left.DeviceID.String() > right.DeviceID.String()
}

// holds reports whether a page of the log carries a change to the record.
func holds(operations []*quirev1.Operation, target uuid.UUID) bool {
	for _, operation := range operations {
		if operation.GetTargetId() == target.String() {
			return true
		}
	}

	return false
}

// watch opens the stream and reports what arrives on it, until the returned
// function is called.
func watch(t *testing.T, appliance *device) (arrivals <-chan *quirev1.Operation, stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	arrived := make(chan *quirev1.Operation, 64)

	go func() {
		defer close(arrived)

		_ = appliance.Watch(ctx, client.Watcher{
			Operations: func(operations []*quirev1.Operation) {
				for _, operation := range operations {
					select {
					case arrived <- operation:
					case <-ctx.Done():
						return
					}
				}
			},
		})
	}()

	return arrived, cancel
}

// digestOf is a content hash for a work whose bytes this suite never has.
//
// The node stores what it is told the digest is and checks it only against the
// bytes of an upload, so a work registered without a file is a work with a
// digest nothing has yet answered for — which is the ordinary state of a work
// on a node that replicates the metadata and not the files (D02).
func digestOf(t *testing.T, name string) string {
	t.Helper()

	digest := sha256.Sum256([]byte(name))

	return hex.EncodeToString(digest[:])
}
