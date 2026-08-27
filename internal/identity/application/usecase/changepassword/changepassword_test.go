package changepassword_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/changepassword"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// theOldPassword is what the reader of this file was registered with.
const theOldPassword = "correct horse battery staple"

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case and a reader signed in from two appliances.
type fixture struct {
	usecase     *changepassword.ChangePassword
	users       *apptest.UserRepository
	credentials *apptest.CredentialRepository
	hasher      *apptest.HashService
	reader      *user.User
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	users := apptest.NewUserRepository()
	devices := apptest.NewDeviceRepository()
	credentials := apptest.NewCredentialRepository()
	hasher := apptest.NewHashService()
	auth := apptest.NewAuthService()
	server := apptest.NewLocalServer("quire-a.example")
	clock := apptest.NewClock(now())

	registered, err := register.New(users, hasher, server, clock).Execute(t.Context(), register.Input{
		LocalName:   "anthony",
		DisplayName: "Anthony",
		Email:       "anthony@example.test",
		Password:    theOldPassword,
	})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	signIn := login.New(users, devices, credentials, hasher, auth, server, clock, apptest.NewTransaction())

	for _, name := range []string{"Pixel 9", "Tablet"} {
		_, err = signIn.Execute(t.Context(), login.Input{
			LocalName: "anthony",
			Password:  theOldPassword,
			Device:    login.Binding{Name: name, Platform: "android"},
		})
		if err != nil {
			t.Fatalf("logging in %s: %v", name, err)
		}
	}

	return fixture{
		usecase:     changepassword.New(users, credentials, hasher, clock, apptest.NewTransaction()),
		users:       users,
		credentials: credentials,
		hasher:      hasher,
		reader:      registered.User,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), changepassword.Input{
		UserID:          f.reader.ID,
		CurrentPassword: theOldPassword,
		NewPassword:     "a different password entirely",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := f.users.GetByID(t.Context(), f.reader.ID)
	if err != nil {
		t.Fatalf("the reader: %v", err)
	}

	matched, err := f.hasher.Verify("a different password entirely", stored.PasswordHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !matched {
		t.Error("the stored digest is not the one the new password produces")
	}
}

// TestExecuteEndsEverySession is the part the contract leaves unsaid: a reader
// who changes their password is responding to a suspicion, and a session that
// survived would be the one they suspect.
func TestExecuteEndsEverySession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Fatalf("%d sessions are live before the change, want 2", live)
	}

	_, err := f.usecase.Execute(t.Context(), changepassword.Input{
		UserID:          f.reader.ID,
		CurrentPassword: theOldPassword,
		NewPassword:     "a different password entirely",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 0 {
		t.Errorf("%d sessions are live after the change, want none on any device", live)
	}
}

// TestExecuteRefusesTheWrongCurrentPassword is the check the contract asks for:
// a session proves a device is unlocked, not that the reader is at it.
func TestExecuteRefusesTheWrongCurrentPassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), changepassword.Input{
		UserID:          f.reader.ID,
		CurrentPassword: "not the password",
		NewPassword:     "a different password entirely",
	})
	if err == nil {
		t.Fatal("Execute with the wrong current password = nil, want an error")
	}

	if !errors.Is(err, errs.KindUnauthenticated) {
		t.Errorf("error = %v, want unauthenticated", err)
	}

	if code := errs.CodeOf(err); code != changepassword.CodeWrongPassword {
		t.Errorf("code = %q, want %q", code, changepassword.CodeWrongPassword)
	}

	// And nothing was ended: a wrong guess must not cost the reader their
	// sessions, or the call becomes a way to log somebody out.
	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Errorf("%d sessions are live after a refused change, want both", live)
	}
}

// TestExecuteChecksTheNewPasswordFirst keeps a password the node would refuse
// from costing anything, and from being an oracle for the old one.
func TestExecuteChecksTheNewPasswordFirst(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), changepassword.Input{
		UserID:          f.reader.ID,
		CurrentPassword: "not the password",
		NewPassword:     "short",
	})
	if err == nil {
		t.Fatal("Execute with a password under the floor = nil, want an error")
	}

	// The reply is about the new password, not about the current one, so a
	// caller cannot use a deliberately bad new password to probe the old.
	if code := errs.CodeOf(err); code != user.CodeInvalidPassword {
		t.Errorf("code = %q, want %q", code, user.CodeInvalidPassword)
	}
}
