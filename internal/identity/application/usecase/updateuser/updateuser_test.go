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

	name := "  Anthony Muller "

	output, err := f.usecase.Execute(t.Context(), updateuser.Input{
		UserID:      f.reader.ID,
		DisplayName: &name,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.DisplayName != "Anthony Muller":
		t.Errorf("DisplayName = %q, want it trimmed", output.User.DisplayName)
	case output.User.Email != f.reader.Email:
		t.Error("the address changed, and this call no longer writes it (C14)")
	case output.User.LocalName != f.reader.LocalName:
		t.Error("the local name changed, and it is half the identifier RN09 makes unique")
	case !output.User.UpdatedAt.After(f.reader.UpdatedAt):
		t.Error("the instant of the last change did not move")
	case output.FederatedID.String() != "@anthony:quire-a.example":
		t.Errorf("FederatedID = %q, want it unchanged", output.FederatedID)
	}
}

// TestExecuteRefusesADisplayNameTheDomainWill keeps a request the entity would
// reject from reaching the repository at all.
func TestExecuteRefusesADisplayNameTheDomainWill(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	name := "   "

	_, err := f.usecase.Execute(t.Context(), updateuser.Input{UserID: f.reader.ID, DisplayName: &name})
	if err == nil {
		t.Fatal("Execute with a blank name = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	stored, storedErr := f.users.GetByID(t.Context(), f.reader.ID)
	if storedErr != nil {
		t.Fatalf("the reader: %v", storedErr)
	}

	if stored.DisplayName != f.reader.DisplayName {
		t.Errorf("DisplayName = %q, want the record unchanged", stored.DisplayName)
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
