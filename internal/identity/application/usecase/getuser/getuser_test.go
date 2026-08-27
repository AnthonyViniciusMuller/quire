package getuser_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/getuser"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// registered is a reader on this node, and the use case that reads them back.
func registered(t *testing.T) (*getuser.GetUser, *user.User) {
	t.Helper()

	users := apptest.NewUserRepository()
	server := apptest.NewLocalServer("quire-a.example")

	output, err := register.New(users, apptest.NewHashService(), server, apptest.NewClock(now())).
		Execute(t.Context(), register.Input{
			LocalName:   "anthony",
			DisplayName: "Anthony",
			Email:       "anthony@example.test",
			Password:    "correct horse battery staple",
		})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	return getuser.New(users, server), output.User
}

func TestExecute(t *testing.T) {
	t.Parallel()

	usecase, reader := registered(t)

	output, err := usecase.Execute(t.Context(), getuser.Input{UserID: reader.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.ID != reader.ID:
		t.Error("another reader came back")
	case output.User.Email.IsZero():
		t.Error("the address is missing, and this is the one reply that may carry it (RN09)")
	case output.FederatedID.String() != "@anthony:quire-a.example":
		t.Errorf("FederatedID = %q, want it assembled from the record and this node's domain",
			output.FederatedID)
	}
}

// TestExecuteForAReaderWhoIsNotHere covers the record that is gone: the session
// outlived it, which is what a deletion on another connection looks like.
func TestExecuteForAReaderWhoIsNotHere(t *testing.T) {
	t.Parallel()

	usecase, _ := registered(t)

	_, err := usecase.Execute(t.Context(), getuser.Input{UserID: uuid.New()})
	if err == nil {
		t.Fatal("Execute for a reader who is not here = nil, want an error")
	}

	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("error = %v, want not found", err)
	}
}
