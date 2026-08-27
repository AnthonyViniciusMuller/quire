package deleteuser_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/deleteuser"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// thePassword is what the reader of this file was registered with.
const thePassword = "correct horse battery staple"

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case and the reader it removes.
type fixture struct {
	usecase *deleteuser.DeleteUser
	users   *apptest.UserRepository
	reader  *user.User
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	users := apptest.NewUserRepository()
	hasher := apptest.NewHashService()
	server := apptest.NewLocalServer("quire-a.example")

	registered, err := register.New(users, hasher, server, apptest.NewClock(now())).
		Execute(t.Context(), register.Input{
			LocalName:   "anthony",
			DisplayName: "Anthony",
			Email:       "anthony@example.test",
			Password:    thePassword,
		})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	return fixture{usecase: deleteuser.New(users, hasher), users: users, reader: registered.User}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), deleteuser.Input{UserID: f.reader.ID, Password: thePassword})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := f.users.GetByID(t.Context(), f.reader.ID); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("the reader is still here: %v", err)
	}
}

// TestExecuteRefusesTheWrongPassword is the check that separates a deletion from
// a device somebody left unlocked, and what it costs to be wrong is why the
// check is here at all.
func TestExecuteRefusesTheWrongPassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), deleteuser.Input{UserID: f.reader.ID, Password: "not the password"})
	if err == nil {
		t.Fatal("Execute with the wrong password = nil, want an error")
	}

	if !errors.Is(err, errs.KindUnauthenticated) {
		t.Errorf("error = %v, want unauthenticated", err)
	}

	if code := errs.CodeOf(err); code != deleteuser.CodeWrongPassword {
		t.Errorf("code = %q, want %q", code, deleteuser.CodeWrongPassword)
	}

	if _, err := f.users.GetByID(t.Context(), f.reader.ID); err != nil {
		t.Errorf("the reader was removed by a refused call: %v", err)
	}
}

// TestExecuteFreesTheIdentifier is what deletion has to mean for RN09: the pair
// the reader held is available again.
func TestExecuteFreesTheIdentifier(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), deleteuser.Input{UserID: f.reader.ID, Password: thePassword})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	server := apptest.NewLocalServer("quire-a.example")
	server.ServerID = f.reader.OriginServerID

	_, err = register.New(f.users, apptest.NewHashService(), server, apptest.NewClock(now())).
		Execute(t.Context(), register.Input{
			LocalName:   "anthony",
			DisplayName: "Somebody else",
			Email:       "anthony@example.test",
			Password:    thePassword,
		})
	if err != nil {
		t.Errorf("registering the same identifier after the deletion: %v", err)
	}
}
