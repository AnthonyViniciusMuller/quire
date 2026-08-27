package updateuser_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/updateuser"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case, the reader it changes, and a second reader whose
// address is already taken.
type fixture struct {
	usecase *updateuser.UpdateUser
	users   *apptest.UserRepository
	clock   *apptest.Clock
	reader  *user.User
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	users := apptest.NewUserRepository()
	server := apptest.NewLocalServer("quire-a.example")
	clock := apptest.NewClock(now())
	registrar := register.New(users, apptest.NewHashService(), server, clock)

	output, err := registrar.Execute(t.Context(), register.Input{
		LocalName:   "anthony",
		DisplayName: "Anthony",
		Email:       "anthony@example.test",
		Password:    "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	_, err = registrar.Execute(t.Context(), register.Input{
		LocalName:   "somebody",
		DisplayName: "Somebody",
		Email:       "taken@example.test",
		Password:    "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("registering the second reader: %v", err)
	}

	return fixture{
		usecase: updateuser.New(users, server, clock),
		users:   users,
		clock:   clock,
		reader:  output.User,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.clock.Advance(time.Hour)

	name, address := "  Anthony Muller ", "anthony@other.test"

	output, err := f.usecase.Execute(t.Context(), updateuser.Input{
		UserID:      f.reader.ID,
		DisplayName: &name,
		Email:       &address,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.DisplayName != "Anthony Muller":
		t.Errorf("DisplayName = %q, want it trimmed", output.User.DisplayName)
	case output.User.Email != "anthony@other.test":
		t.Errorf("Email = %q, want the new one", output.User.Email)
	case output.User.LocalName != f.reader.LocalName:
		t.Error("the local name changed, and it is half the identifier RN09 makes unique")
	case !output.User.UpdatedAt.After(f.reader.UpdatedAt):
		t.Error("the instant of the last change did not move")
	case output.FederatedID.String() != "@anthony:quire-a.example":
		t.Errorf("FederatedID = %q, want it unchanged", output.FederatedID)
	}
}

// TestExecuteLeavesTheFieldsItWasNotGiven is what the pointers are for: absence
// is not emptiness.
func TestExecuteLeavesTheFieldsItWasNotGiven(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	name := "Anthony M."

	output, err := f.usecase.Execute(t.Context(), updateuser.Input{UserID: f.reader.ID, DisplayName: &name})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.User.Email != f.reader.Email {
		t.Errorf("Email = %q, want it left alone", output.User.Email)
	}
}

// TestExecuteWritesNothingWhenOneFieldIsBad keeps a request with one good field
// and one bad one from changing half the record.
func TestExecuteWritesNothingWhenOneFieldIsBad(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	name, address := "Anthony M.", "not an address"

	_, err := f.usecase.Execute(t.Context(), updateuser.Input{
		UserID:      f.reader.ID,
		DisplayName: &name,
		Email:       &address,
	})
	if err == nil {
		t.Fatal("Execute with an address that is not one = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	stored, err := f.users.GetByID(t.Context(), f.reader.ID)
	if err != nil {
		t.Fatalf("the reader: %v", err)
	}

	if stored.DisplayName != f.reader.DisplayName {
		t.Errorf("DisplayName = %q, want the record unchanged", stored.DisplayName)
	}
}

// TestExecuteRefusesAnAddressAlreadyRegistered is RN09 on the update path: the
// address is unique within the origin server however it got there.
func TestExecuteRefusesAnAddressAlreadyRegistered(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	address := "TAKEN@example.test"

	_, err := f.usecase.Execute(t.Context(), updateuser.Input{UserID: f.reader.ID, Email: &address})
	if err == nil {
		t.Fatal("taking another reader's address = nil, want an error")
	}

	if !errors.Is(err, errs.KindAlreadyExists) {
		t.Errorf("error = %v, want already exists", err)
	}

	if code := errs.CodeOf(err); code != user.CodeEmailRegistered {
		t.Errorf("code = %q, want %q", code, user.CodeEmailRegistered)
	}
}

// TestExecuteWithoutAField is answered rather than treated as a write of
// nothing: a client that meant to change something and sent an empty mask
// should be told.
func TestExecuteWithoutAField(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), updateuser.Input{UserID: f.reader.ID})
	if err == nil {
		t.Fatal("Execute naming no field = nil, want an error")
	}

	if code := errs.CodeOf(err); code != updateuser.CodeNothingToUpdate {
		t.Errorf("code = %q, want %q", code, updateuser.CodeNothingToUpdate)
	}
}
