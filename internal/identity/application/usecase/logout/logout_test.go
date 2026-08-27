package logout_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/logout"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// testDomain is the node the reader of this file is registered on.
const testDomain user.ServerDomain = "quire-a.example"

// thePassword is the one that reader was registered with.
const thePassword = "correct horse battery staple"

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case, and a reader already logged in from two appliances —
// since ending one session is only meaningful when there is another to leave
// alone (RN07).
type fixture struct {
	usecase     *logout.Logout
	credentials *apptest.CredentialRepository
	auth        *apptest.AuthService
	reader      *user.User
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
	server := apptest.NewLocalServer(testDomain)
	clock := apptest.NewClock(now())

	registered, err := register.New(users, hasher, server, clock).Execute(t.Context(), register.Input{
		LocalName:   "anthony",
		DisplayName: "Anthony",
		Email:       "anthony@example.test",
		Password:    thePassword,
	})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	signIn := login.New(users, devices, credentials, hasher, auth, server, clock, apptest.NewTransaction())

	phone, err := signIn.Execute(t.Context(), login.Input{
		LocalName: "anthony",
		Password:  thePassword,
		Device:    login.Binding{Name: "Pixel 9", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("logging in the phone: %v", err)
	}

	tablet, err := signIn.Execute(t.Context(), login.Input{
		LocalName: "anthony",
		Password:  thePassword,
		Device:    login.Binding{Name: "Tablet", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("logging in the tablet: %v", err)
	}

	return fixture{
		usecase:     logout.New(credentials, auth),
		credentials: credentials,
		auth:        auth,
		reader:      registered.User,
		phone:       phone,
		tablet:      tablet,
	}
}

// TestExecuteEndsOneSession is RN07: a reader uses several appliances at once,
// and the one that logs out is the one that presented the credential.
func TestExecuteEndsOneSession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Fatalf("%d sessions are live before the logout, want 2", live)
	}

	if _, err := f.usecase.Execute(t.Context(), logout.Input{RefreshToken: f.phone.Session.RefreshToken}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 1 {
		t.Errorf("%d sessions are live after the logout, want the tablet's to survive (RN07)", live)
	}

	spent, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(f.phone.Session.RefreshToken))
	if err != nil {
		t.Fatalf("the credential is gone rather than spent: %v", err)
	}

	if spent.Usable(now()) {
		t.Error("the credential the caller presented is still usable")
	}

	survivor, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(f.tablet.Session.RefreshToken))
	if err != nil {
		t.Fatalf("the other device's credential: %v", err)
	}

	if !survivor.Usable(now()) {
		t.Error("logging one device out ended another device's session")
	}
}

// TestExecuteIsIdempotent is the rule RFC 7009 states for revocation: a
// credential that no longer works already satisfies what the caller asked for,
// and a device retrying after a lost reply must not be told its own successful
// logout failed.
func TestExecuteIsIdempotent(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	input := logout.Input{RefreshToken: f.phone.Session.RefreshToken}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("the first logout: %v", err)
	}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Errorf("the second logout: %v, want it to succeed", err)
	}
}

// TestExecuteAcceptsACredentialItNeverIssued is the same rule for a value that
// was never a credential at all.
func TestExecuteAcceptsACredentialItNeverIssued(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), logout.Input{RefreshToken: "not a credential"}); err != nil {
		t.Errorf("Execute with an unknown credential: %v, want it to succeed", err)
	}

	// And it must not have ended anything.
	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Errorf("%d sessions are live, want both", live)
	}
}

// TestExecuteWillNotSpendARecoveryCredential stops a call anybody may make from
// becoming a way to cancel somebody's password recovery.
func TestExecuteWillNotSpendARecoveryCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	secret, err := f.auth.IssueRecovery(now())
	if err != nil {
		t.Fatalf("IssueRecovery: %v", err)
	}

	recovery, err := credential.NewRecovery(f.reader.ID, secret.Digest, secret.ExpiresAt)
	if err != nil {
		t.Fatalf("NewRecovery: %v", err)
	}

	err = f.credentials.Create(t.Context(), recovery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = f.usecase.Execute(t.Context(), logout.Input{RefreshToken: secret.Value})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := f.credentials.GetByTokenHash(t.Context(), secret.Digest)
	if err != nil {
		t.Fatalf("the recovery credential: %v", err)
	}

	if !stored.Usable(now()) {
		t.Error("logging out spent a password recovery credential")
	}
}

// TestExecuteWithoutACredential is the one shape answered as a malformed
// request, because unlike an unrecognized credential it says nothing about any
// session.
func TestExecuteWithoutACredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), logout.Input{})
	if err == nil {
		t.Fatal("Execute presenting nothing = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != logout.CodeNoCredential {
		t.Errorf("code = %q, want %q", code, logout.CodeNoCredential)
	}
}
