//go:build e2e

package e2e_test

import (
	"errors"
	"strings"
	"testing"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/client"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	userrepository "github.com/anthonyvsmuller/quire/internal/identity/infra/repository/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// UC16 and RF17, end to end and without the previous node taking part in it.
//
// The reader moves to a node they had no account on, carrying the devices they
// already had, and the devices go on writing where they left off: what each of
// them was holding and had not handed over is pushed to the new node and
// applied there, under the identifiers it was authored with.
//
// C11 is what makes that possible and what this test is really about. The
// devices are adopted with the identifiers they already hold, because every
// operation names its authoring device and every vector clock is keyed by a
// device id — a node that minted fresh ones would hold a history naming devices
// that do not exist, and two devices that had been in sync would read as
// concurrent for ever. What the migration cannot do is prove the previous
// identity: the reader's identifier changes, and the one they arrived under is
// recorded as provenance and never as identity.
func TestAReaderMovesToAnotherHomeServerWithTheirDevices(t *testing.T) {
	who := newReader(t, nodeA)
	tablet := newDevice(t, nodeA, who, "tablet")
	phone := newDevice(t, nodeA, who, "phone")

	previous := "@" + who.localName + ":" + nodeA.domain

	// What each device is holding when the reader decides to move. Neither has
	// handed it over, which is the state a migration has to survive: the
	// tablet's work exists nowhere yet, and the phone's mark is about it.
	tablet.disconnect(t)
	work := createWork(t, tablet, "Vidas Secas")

	phone.disconnect(t)

	mark, err := phone.CreateAnnotation(t.Context(), &client.AnnotationInput{
		Ebook:   work,
		Kind:    "highlight",
		Locator: "epubcfi(/6/14!/4/2/2)",
		Text:    "a cachorra Baleia",
	})
	if err != nil {
		t.Fatalf("the phone writing offline: %v", err)
	}

	// The call is addressed to the node being moved *to*, by a device that
	// already holds the reader's collection — which is what lets it proceed
	// without the previous node's cooperation or availability.
	tablet.moveTo(t, nodeB)

	arrived, err := tablet.Migrate(t.Context(), &client.Migration{
		LocalName:           who.localName,
		DisplayName:         "A reader who moved",
		Email:               who.email,
		Password:            thePassword,
		PreviousFederatedID: previous,
		Devices:             []client.Device{phone.State().Device},
	})
	if err != nil {
		t.Fatalf("migrating to %s: %v", nodeB.domain, err)
	}

	if identifier := arrived.GetUser().GetFederatedId(); !strings.HasSuffix(identifier, nodeB.domain) {
		t.Errorf("the reader arrived as %s, which is not hosted on %s", identifier, nodeB.domain)
	}

	if identifier := arrived.GetUser().GetFederatedId(); identifier == previous {
		t.Error("the identifier did not change, which it must: the server half is the new node")
	}

	// The devices, with the identifiers they already held. A client is told to
	// compare what it sent against what came back, and this is that check.
	adopted := map[string]bool{}
	for _, appliance := range arrived.GetDevices() {
		adopted[appliance.GetId()] = true
	}

	for _, appliance := range []*device{tablet, phone} {
		if !adopted[appliance.State().Device.ID.String()] {
			t.Errorf("%s was not adopted with the identifier it already had", appliance.name)
		}
	}

	// Provenance and never identity: the node records what it was told and
	// cannot check it, so nothing authenticates against it and it appears in
	// no reply. The database is the only place it is visible, which is itself
	// the point.
	if recorded := provenanceOf(t, nodeB, arrived.GetUser().GetId()); recorded != previous {
		t.Errorf("%s recorded the reader as arriving from %q, want %q",
			nodeB.domain, recorded, previous)
	}

	// The session came back for the device that made the call (C20), so the
	// tablet can hand over its backlog immediately.
	push(t, tablet)

	// The other devices log in as they normally would, with the identifier
	// they were adopted under, and what they were holding is still theirs.
	phone.moveTo(t, nodeB)

	if _, err = phone.Login(t.Context(), &client.Credentials{
		LocalName:      who.localName,
		Password:       thePassword,
		DeviceName:     phone.name,
		DevicePlatform: "e2e",
	}); err != nil {
		t.Fatalf("the phone logging in to %s: %v", nodeB.domain, err)
	}

	if held := len(phone.Pending()); held != 1 {
		t.Fatalf("the phone holds %d changes after moving, want the one it authored", held)
	}

	push(t, phone)

	// Both changes are on the new node, under the identifiers they were
	// authored with, and the mark still points at the work it was made in.
	if held := title(t, tablet, work); held != "Vidas Secas" {
		t.Errorf("the work reads as %q on %s", held, nodeB.domain)
	}

	marks, _, err := tablet.ListAnnotations(t.Context(), work, 0, "")
	if err != nil {
		t.Fatalf("reading the marks on %s: %v", nodeB.domain, err)
	}

	if len(marks) != 1 || marks[0].GetId() != mark.Target.String() {
		t.Errorf("the work carries %d marks on %s, want the one the phone wrote",
			len(marks), nodeB.domain)
	}

	// The previous node was not asked anything and did not lose anything,
	// which is what makes the migration independent of it. A device binding
	// there still finds the reader it always had.
	stayed := newDevice(t, nodeA, who, "borrowed")

	reader, err := stayed.Whoami(t.Context())
	if err != nil {
		t.Fatalf("reading the reader still on %s: %v", nodeA.domain, err)
	}

	if reader.GetFederatedId() != previous {
		t.Errorf("%s now knows the reader as %s", nodeA.domain, reader.GetFederatedId())
	}
}

// A migration that carries no devices is refused rather than accepted into a
// history nobody could continue.
//
// The reference client always sends the device making the call, so this one is
// assembled by hand: what is being checked is the node's rule and not the
// client's care.
func TestAMigrationThatCarriesNoDeviceIsRefused(t *testing.T) {
	who := newReader(t, nodeA)

	federation := quirev1.NewFederationServiceClient(dial(t, nodeB))

	_, err := federation.MigrateHomeServer(t.Context(), &quirev1.MigrateHomeServerRequest{
		LocalName:           who.localName,
		DisplayName:         "A reader who brought nothing",
		Email:               who.email,
		Password:            thePassword,
		PreviousFederatedId: "@" + who.localName + ":" + nodeA.domain,
	})

	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("MigrateHomeServer = %v, want an invalid argument", err)
	}
}

// provenanceOf reads what a node recorded about where a reader arrived from.
func provenanceOf(t *testing.T, on *node, userID string) string {
	t.Helper()

	readers := userrepository.New(persist.NewManager(connect(t, on)))

	reader, err := readers.GetByID(t.Context(), uuid.MustParse(userID))
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			t.Fatalf("%s does not hold the reader it just created", on.domain)
		}

		t.Fatalf("reading the reader on %s: %v", on.domain, err)
	}

	return string(reader.MigratedFrom)
}
