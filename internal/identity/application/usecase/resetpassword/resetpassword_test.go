package resetpassword_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/requestrecovery"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/resetpassword"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// theOldPassword is what the reader of this file was registered with, and what
// the reset has to replace.
const theOldPassword = "correct horse battery staple"

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case, a reader signed in from two appliances, and the
// recovery message they have just been sent.
type fixture struct {
	usecase     *resetpassword.ResetPassword
	users       *apptest.UserRepository
	credentials *apptest.CredentialRepository
	hasher      *apptest.HashService
	auth        *apptest.AuthService
	clock       *apptest.Clock
	reader      *user.User
	token       string
	phone       login.Output
	tablet      login.Output
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	users := apptest.NewUserRepository()
	devices := apptest.NewDeviceRepository()
	credentials := apptest.NewCredentialRepository()
	hasher := apptest.NewHashService()
	auth := apptest.NewAuthService()
	mailer := apptest.NewMailer()
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

	phone, err := signIn.Execute(t.Context(), login.Input{
		LocalName: "anthony",
		Password:  theOldPassword,
		Device:    login.Binding{Name: "Pixel 9", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("logging in the phone: %v", err)
	}

	tablet, err := signIn.Execute(t.Context(), login.Input{
		LocalName: "anthony",
		Password:  theOldPassword,
		Device:    login.Binding{Name: "Tablet", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("logging in the tablet: %v", err)
	}

	request := requestrecovery.New(users, credentials, auth, mailer, server, clock, apptest.NewTransaction())

	_, err = request.Execute(t.Context(), requestrecovery.Input{Email: "anthony@example.test"})
	if err != nil {
		t.Fatalf("requesting the recovery: %v", err)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d recovery messages were sent, want 1", len(sent))
	}

	return fixture{
		usecase:     resetpassword.New(users, credentials, hasher, auth, clock, apptest.NewTransaction()),
		users:       users,
		credentials: credentials,
		hasher:      hasher,
		auth:        auth,
		clock:       clock,
		reader:      registered.User,
		token:       sent[0].Token,
		phone:       phone,
		tablet:      tablet,
	}
}

func TestExecuteSetsThePassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), resetpassword.Input{
		RecoveryToken: f.token,
		NewPassword:   "a different password entirely",
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

	old, err := f.hasher.Verify(theOldPassword, stored.PasswordHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if old {
		t.Error("the old password still verifies")
	}
}

// TestExecuteEndsEverySession is why the reset is not merely a password change:
// the reader is recovering because they may not be the only party holding the
// old password.
func TestExecuteEndsEverySession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Fatalf("%d sessions are live before the reset, want 2", live)
	}

	_, err := f.usecase.Execute(t.Context(), resetpassword.Input{
		RecoveryToken: f.token,
		NewPassword:   "a different password entirely",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 0 {
		t.Errorf("%d sessions are live after the reset, want none on any device", live)
	}

	// The recovery credential itself is spent, so the message cannot be
	// replayed.
	if live := f.credentials.Live(credential.KindPasswordRecovery, now()); live != 0 {
		t.Errorf("%d recovery credentials are live after the reset, want none", live)
	}
}

// TestExecuteIsNotReplayable covers the same message presented twice.
func TestExecuteIsNotReplayable(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	input := resetpassword.Input{RecoveryToken: f.token, NewPassword: "a different password entirely"}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("the first reset: %v", err)
	}

	_, err := f.usecase.Execute(t.Context(), input)
	if err == nil {
		t.Fatal("presenting the same recovery twice = nil, want an error")
	}

	if !errors.Is(err, errs.KindUnauthenticated) {
		t.Errorf("error = %v, want unauthenticated", err)
	}
}

// TestExecuteRejectsAPasswordItWouldRefuseWithoutSpendingTheCredential matters
// because there is only one recovery message and it cannot be sent again.
func TestExecuteRejectsAPasswordItWouldRefuseWithoutSpendingTheCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), resetpassword.Input{RecoveryToken: f.token, NewPassword: "short"})
	if err == nil {
		t.Fatal("resetting to a password under the floor = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != user.CodeInvalidPassword {
		t.Errorf("code = %q, want %q", code, user.CodeInvalidPassword)
	}

	stored, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(f.token))
	if err != nil {
		t.Fatalf("the recovery credential: %v", err)
	}

	if !stored.Usable(now()) {
		t.Error("a refused password spent the credential, and the reader cannot be sent another")
	}
}

func TestExecuteRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token func(f *fixture) string
	}{
		{
			name:  "a credential this node never issued",
			token: func(_ *fixture) string { return "not a credential" },
		},
		{
			// Accepting one here would let a device that merely holds a session
			// set the password, which is what ChangePassword asks for the
			// current password instead.
			name:  "a session credential",
			token: func(f *fixture) string { return f.phone.Session.RefreshToken },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			_, err := f.usecase.Execute(t.Context(), resetpassword.Input{
				RecoveryToken: test.token(&f),
				NewPassword:   "a different password entirely",
			})
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if code := errs.CodeOf(err); code != resetpassword.CodeInvalidCredential {
				t.Errorf("code = %q, want %q", code, resetpassword.CodeInvalidCredential)
			}
		})
	}
}

// TestExecuteRejectsAnExpiredCredential is what the shorter recovery lifetime is
// for: the credential travels through a mailbox, which is not a channel that
// stays private for a month.
func TestExecuteRejectsAnExpiredCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	// Past the hour the recovery lifetime defaults to.
	f.clock.Advance(2 * time.Hour)

	_, err := f.usecase.Execute(t.Context(), resetpassword.Input{
		RecoveryToken: f.token,
		NewPassword:   "a different password entirely",
	})
	if err == nil {
		t.Fatal("resetting with an expired credential = nil, want an error")
	}

	if code := errs.CodeOf(err); code != resetpassword.CodeInvalidCredential {
		t.Errorf("code = %q, want %q", code, resetpassword.CodeInvalidCredential)
	}
}

// TestExecuteWithoutACredential is a malformed request rather than a failed
// recovery.
func TestExecuteWithoutACredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), resetpassword.Input{NewPassword: "a different password entirely"})
	if err == nil {
		t.Fatal("Execute presenting nothing = nil, want an error")
	}

	if code := errs.CodeOf(err); code != resetpassword.CodeNoCredential {
		t.Errorf("code = %q, want %q", code, resetpassword.CodeNoCredential)
	}
}
