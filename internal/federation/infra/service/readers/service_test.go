package readers_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/service/readers"
	identityapptest "github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	identityuser "github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// admitted is when the readers below arrive.
var admitted = time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

type fixture struct {
	adapter *readers.Service
	users   *identityapptest.UserRepository
	devices *identityapptest.DeviceRepository
	origin  uuid.UUID
	reader  service.Reader
	phone   service.Device
}

func newFixture() *fixture {
	users := identityapptest.NewUserRepository()
	devices := identityapptest.NewDeviceRepository()

	return &fixture{
		adapter: readers.New(users, devices, apptest.NewClock(admitted)),
		users:   users,
		devices: devices,
		origin:  uuid.New(),
		reader:  service.Reader{ID: uuid.New(), LocalName: "guimaraes", DisplayName: "Guimarães Rosa"},
		phone:   service.Device{ID: uuid.New(), Name: "the phone", Platform: "android"},
	}
}

func TestAdmitRecordsAReaderWithNoPasswordAndTheirDevices(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if err := f.adapter.Admit(t.Context(), f.origin, &f.reader, []service.Device{f.phone}); err != nil {
		t.Fatalf("Admit: %v", err)
	}

	held, err := f.users.GetByID(t.Context(), f.reader.ID)
	if err != nil {
		t.Fatalf("the reader was not recorded: %v", err)
	}

	switch {
	case held.OriginServerID != f.origin:
		t.Error("the reader is not recorded as the origin's")
	case held.Authenticates():
		t.Error("a replicated reader was given a password, and this node would authenticate them")
	case !held.Email.IsZero():
		t.Error("the address travelled, which RN09 keeps out of the replicated set")
	case held.LocalName.String() != "guimaraes":
		t.Errorf("the reader is called %s here", held.LocalName)
	}

	appliance, err := f.devices.GetByID(t.Context(), f.phone.ID)
	if err != nil {
		t.Fatalf("the device was not recorded: %v", err)
	}

	if !appliance.BelongsTo(f.reader.ID) || !appliance.Active {
		t.Errorf("the device was recorded as %+v", appliance.Props)
	}
}

// Admission is a standing obligation and not a handshake: the second call
// adds the device bound since and leaves the rest as it was.
func TestAdmitAgainAddsWhatIsNewAndLeavesTheRest(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if err := f.adapter.Admit(t.Context(), f.origin, &f.reader, []service.Device{f.phone}); err != nil {
		t.Fatalf("the first admission: %v", err)
	}

	tablet := service.Device{ID: uuid.New(), Name: "the tablet", Platform: "ios"}

	if err := f.adapter.Admit(t.Context(), f.origin, &f.reader, []service.Device{f.phone, tablet}); err != nil {
		t.Fatalf("the second admission: %v", err)
	}

	if f.users.Count() != 1 {
		t.Errorf("the reader is recorded %d times", f.users.Count())
	}

	if f.devices.Count() != 2 {
		t.Errorf("%d devices are recorded, want the phone and the tablet", f.devices.Count())
	}
}

// A claim over a row that has another owner is not one a peer gets to make.
func TestAdmitRefusesWhatAnotherNodeOrReaderHolds(t *testing.T) {
	t.Parallel()

	f := newFixture()

	address, err := identityuser.ParseEmail("somebody@example.org")
	if err != nil {
		t.Fatalf("ParseEmail: %v", err)
	}

	hosted, err := identityuser.New(&identityuser.Props{
		OriginServerID: uuid.New(),
		LocalName:      "guimaraes",
		DisplayName:    "Somebody here",
		Email:          address,
		PasswordHash:   "a digest",
		CreatedAt:      admitted,
		UpdatedAt:      admitted,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = f.users.Create(t.Context(), hosted); err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed := service.Reader{ID: hosted.ID, LocalName: "guimaraes", DisplayName: "Guimarães Rosa"}

	err = f.adapter.Admit(t.Context(), f.origin, &claimed, nil)
	if !errors.Is(err, errs.KindPermissionDenied) || errs.CodeOf(err) != readers.CodeReaderHeld {
		t.Errorf("Admit over a reader hosted here = %v, want it refused", err)
	}

	if err = f.adapter.Admit(t.Context(), f.origin, &f.reader, []service.Device{f.phone}); err != nil {
		t.Fatalf("admitting the reader: %v", err)
	}

	other := service.Reader{ID: uuid.New(), LocalName: "clarice", DisplayName: "Clarice Lispector"}

	err = f.adapter.Admit(t.Context(), f.origin, &other, []service.Device{f.phone})
	if !errors.Is(err, errs.KindPermissionDenied) || errs.CodeOf(err) != readers.CodeDeviceHeld {
		t.Errorf("Admit over another reader's device = %v, want it refused", err)
	}
}

func TestAdmitRefusesANameItCannotParse(t *testing.T) {
	t.Parallel()

	f := newFixture()
	unnamed := service.Reader{ID: uuid.New(), LocalName: "", DisplayName: "Nobody"}

	if err := f.adapter.Admit(t.Context(), f.origin, &unnamed, nil); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Admit = %v, want an invalid argument", err)
	}

	if f.users.Count() != 0 {
		t.Error("a reader with no name was recorded")
	}
}
