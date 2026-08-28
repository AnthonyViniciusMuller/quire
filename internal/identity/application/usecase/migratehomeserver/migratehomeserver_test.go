package migratehomeserver_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/migratehomeserver"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// arrived is when the migrations below happen.
var arrived = time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

// fixture is the use case over the slice's doubles.
type fixture struct {
	usecase *migratehomeserver.MigrateHomeServer
	users   *apptest.UserRepository
	devices *apptest.DeviceRepository

	phone  uuid.UUID
	tablet uuid.UUID
}

func newFixture() *fixture {
	users := apptest.NewUserRepository()
	devices := apptest.NewDeviceRepository()

	return &fixture{
		usecase: migratehomeserver.New(
			users,
			devices,
			apptest.NewCredentialRepository(),
			apptest.NewHashService(),
			apptest.NewAuthService(),
			apptest.NewLocalServer("quire-b.example"),
			apptest.NewClock(arrived),
			apptest.NewTransaction(),
		),
		users:   users,
		devices: devices,
		phone:   uuid.New(),
		tablet:  uuid.New(),
	}
}

// input is a reader arriving with two devices, the phone first.
func (f *fixture) input() migratehomeserver.Input {
	return migratehomeserver.Input{
		LocalName:           "tony",
		DisplayName:         "Tony",
		Email:               "tony@example.com",
		Password:            "a-long-enough-password",
		PreviousFederatedID: "@tony:quire-a.example",
		Devices: []migratehomeserver.Arrival{
			{ID: f.phone.String(), Name: "phone", Platform: "android"},
			{ID: f.tablet.String(), Name: "tablet", Platform: "ipados"},
		},
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.LocalName.String() != "tony":
		t.Errorf("the reader arrived as %q", output.User.LocalName)
	case output.FederatedID.String() != "@tony:quire-b.example":
		t.Errorf("the identifier is %q, want the domain half to be this node", output.FederatedID)
	case output.Session.AccessToken == "":
		t.Error("no session came back, so the calling device cannot begin pushing")
	case len(output.Devices) != 2:
		t.Fatalf("%d devices were adopted, want both", len(output.Devices))
	}
}

// This is the whole of C11. Every operation in the reader's history names the
// device that authored it and every vector clock is keyed by one, so a node
// that minted new identifiers would import a history naming devices that do
// not exist there.
func TestExecuteAdoptsTheDevicesWithTheIdentifiersTheyAlreadyHold(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, arriving := range []uuid.UUID{f.phone, f.tablet} {
		adopted, getErr := f.devices.GetByID(t.Context(), arriving)
		if getErr != nil {
			t.Fatalf("the device was not adopted under the identifier it holds: %v", getErr)
		}

		if !adopted.BelongsTo(output.User.ID) {
			t.Error("the device was adopted under somebody else")
		}

		if !adopted.Active {
			t.Error("the device was adopted already unbound")
		}
	}
}

// The session is for the calling device, and the first in the list is the one
// making the call: the contract carries no field saying which (C20).
func TestExecuteIssuesTheSessionForTheFirstDevice(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Devices[0].ID != f.phone {
		t.Errorf("the first device adopted is %s, want the one the client listed first", output.Devices[0].ID)
	}
}

// The identifier changes whatever the local name turns out to be: the domain
// half is this node. What is preserved is the data and not the name, and the
// claim about where the reader came from is recorded rather than believed.
func TestExecuteRecordsThePreviousIdentifierWithoutBelievingIt(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := output.User.MigratedFrom.String(); got != "@tony:quire-a.example" {
		t.Errorf("the provenance came out as %q", got)
	}

	if output.User.OriginServerID == (uuid.UUID{}) {
		t.Error("the reader was not bound to this node, which is what UC16 changes")
	}
}

// A migration carrying no device identities is an account this node can create
// and can never be given anything to hold, because the history about to be
// imported names devices.
func TestExecuteRefusesAMigrationThatCarriesNoDevices(t *testing.T) {
	t.Parallel()

	f := newFixture()
	input := f.input()
	input.Devices = nil

	if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Execute = %v, want an invalid argument", err)
	}
}

// A device that forgot the identifier it has been writing under would start a
// second clock entry that never merges with the first, so it is refused rather
// than given a new one.
func TestExecuteRefusesADeviceThatArrivedWithoutItsIdentifier(t *testing.T) {
	t.Parallel()

	f := newFixture()

	for name, id := range map[string]string{
		"a device carrying no identifier":          "",
		"a device carrying something else":         "the-phone",
		"a device carrying a malformed identifier": "0a0dca4b-7174",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := f.input()
			input.Devices = []migratehomeserver.Arrival{{ID: id, Name: "phone", Platform: "android"}}

			if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Execute = %v, want an invalid argument", err)
			}
		})
	}
}

// Sending a device twice is not an error to the contract, and it is not one
// here: the second is dropped, because adopting it twice would be one row and
// two claims on the same identifier.
func TestExecuteAdoptsADeviceSentTwiceOnce(t *testing.T) {
	t.Parallel()

	f := newFixture()
	input := f.input()
	input.Devices = append(input.Devices, input.Devices[0])

	output, err := f.usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(output.Devices) != 2 {
		t.Errorf("%d devices were adopted, want the repeat dropped", len(output.Devices))
	}
}

// A device identifier is what a vector clock entry is keyed by, so this node
// cannot hold two devices under one — and the refusal says nothing about whose
// the other is.
func TestExecuteRefusesADeviceIdentifierThisNodeAlreadyHolds(t *testing.T) {
	t.Parallel()

	f := newFixture()

	held := device.Restore(f.phone, &device.Props{
		UserID:   uuid.New(),
		Name:     device.Name("somebody else's phone"),
		Platform: device.Platform("android"),
		Active:   true,
	})
	if err := f.devices.Create(t.Context(), held); err != nil {
		t.Fatalf("seeding a device: %v", err)
	}

	if _, err := f.usecase.Execute(t.Context(), f.input()); !errors.Is(err, errs.KindAlreadyExists) {
		t.Errorf("Execute = %v, want an already exists", err)
	}
}

// The local name is this node's to give, so a name already taken here is
// refused exactly as it is on registering — and the reader picks another.
func TestExecuteRefusesALocalNameAlreadyTakenHere(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("the first migration: %v", err)
	}

	second := f.input()
	second.Devices = []migratehomeserver.Arrival{{ID: uuid.New().String(), Name: "laptop", Platform: "linux"}}
	second.Email = "somebody@example.com"

	if _, err := f.usecase.Execute(t.Context(), second); !errors.Is(err, errs.KindAlreadyExists) {
		t.Errorf("Execute = %v, want an already exists", err)
	}
}

// Everything a registration validates, this validates too, and before the
// password is hashed: it is a call an unauthenticated caller can make
// repeatedly.
func TestExecuteRefusesWhatRegistrationWould(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*migratehomeserver.Input){
		"a local name the identifier cannot carry": func(in *migratehomeserver.Input) {
			in.LocalName = "Tony Muller"
		},
		"an address this node cannot store": func(in *migratehomeserver.Input) { in.Email = "not-an-address" },
		"a password outside the bounds":     func(in *migratehomeserver.Input) { in.Password = "short" },
		"a previous identifier that is not one": func(in *migratehomeserver.Input) {
			in.PreviousFederatedID = "tony at quire-a"
		},
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			input := f.input()
			breaks(&input)

			if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Execute = %v, want an invalid argument", err)
			}
		})
	}
}

// A reader who did not migrate carries no provenance, and the column is null
// for them: registering is how an account normally begins.
func TestProvenanceIsOptional(t *testing.T) {
	t.Parallel()

	f := newFixture()
	input := f.input()
	input.PreviousFederatedID = ""

	output, err := f.usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !output.User.MigratedFrom.IsZero() {
		t.Errorf("a migration that claimed nothing recorded %q", output.User.MigratedFrom)
	}
}
