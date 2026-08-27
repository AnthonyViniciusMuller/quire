package refresh_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/refresh"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// thePassword is the one the reader of this file was registered with.
const thePassword = "correct horse battery staple"

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case, and a reader already logged in from two appliances —
// so that a test can tell what a revocation reached from what it left alone.
type fixture struct {
	usecase     *refresh.Refresh
	credentials *apptest.CredentialRepository
	devices     *apptest.DeviceRepository
	auth        *apptest.AuthService
	clock       *apptest.Clock
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
	server := apptest.NewLocalServer("quire-a.example")
	clock := apptest.NewClock(now())

	_, err := register.New(users, hasher, server, clock).Execute(t.Context(), register.Input{
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
		usecase:     refresh.New(credentials, devices, auth, clock, apptest.NewTransaction()),
		credentials: credentials,
		devices:     devices,
		auth:        auth,
		clock:       clock,
		phone:       phone,
		tablet:      tablet,
	}
}

// TestExecuteRotates is D07: the credential presented is spent and the reply
// carries its replacement, rather than the same credential staying valid for
// its whole lifetime.
func TestExecuteRotates(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	old := f.phone.Session.RefreshToken

	output, err := f.usecase.Execute(t.Context(), refresh.Input{RefreshToken: old})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Session.AccessToken == "":
		t.Error("no access token was issued")
	case output.Session.RefreshToken == "":
		t.Error("no replacement credential was issued")
	case output.Session.RefreshToken == old:
		t.Error("the same credential came back, so nothing was rotated")
	case output.Session.AccessToken == f.phone.Session.AccessToken:
		t.Error("the access token was not replaced")
	}

	spent, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(old))
	if err != nil {
		t.Fatalf("the credential presented: %v", err)
	}

	if spent.Usable(now()) {
		t.Error("the credential presented is still usable")
	}

	replacement, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(output.Session.RefreshToken))
	if err != nil {
		t.Fatalf("the replacement was not stored: %v", err)
	}

	if !replacement.BelongsToDevice(f.phone.Device.ID) {
		t.Error("the replacement does not name the device the session belongs to")
	}
}

// TestExecuteRefusesAReusedCredentialAndEndsTheDevicesSessions is the second
// half of D07. A credential presented after it was spent is one two parties
// hold, since the legitimate device already exchanged it.
func TestExecuteRefusesAReusedCredentialAndEndsTheDevicesSessions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	stolen := f.phone.Session.RefreshToken

	// The reader's own device refreshes first, as it would.
	rotated, err := f.usecase.Execute(t.Context(), refresh.Input{RefreshToken: stolen})
	if err != nil {
		t.Fatalf("the first refresh: %v", err)
	}

	// Whoever copied the credential presents it afterwards.
	_, err = f.usecase.Execute(t.Context(), refresh.Input{RefreshToken: stolen})
	if err == nil {
		t.Fatal("presenting a spent credential = nil, want an error")
	}

	if !errors.Is(err, errs.KindUnauthenticated) {
		t.Errorf("error = %v, want unauthenticated", err)
	}

	if code := errs.CodeOf(err); code != refresh.CodeCredentialReused {
		t.Errorf("code = %q, want %q", code, refresh.CodeCredentialReused)
	}

	// The replacement the reader's device is holding is ended too: this node
	// cannot tell which of the two holders is the reader.
	held, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(rotated.Session.RefreshToken))
	if err != nil {
		t.Fatalf("the replacement: %v", err)
	}

	if held.Usable(now()) {
		t.Error("the replacement survived the reuse, so the copy could go on refreshing beside the reader")
	}

	// And no further than that device. The reader's other appliance is not
	// implicated by a credential it never held (RN07).
	other, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(f.tablet.Session.RefreshToken))
	if err != nil {
		t.Fatalf("the other device's credential: %v", err)
	}

	if !other.Usable(now()) {
		t.Error("the reuse ended a session on a device that had nothing to do with it")
	}
}

func TestExecuteRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token func(f *fixture) string
		code  string
	}{
		{
			name:  "a credential this node never issued",
			token: func(_ *fixture) string { return "not a credential" },
			code:  refresh.CodeInvalidCredential,
		},
		{
			name: "a password recovery credential",
			// Exchanging one here would turn the weaker credential of UC08
			// into the stronger one of UC07.
			token: func(f *fixture) string { return f.recoveryCredential(t) },
			code:  refresh.CodeInvalidCredential,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			_, err := f.usecase.Execute(t.Context(), refresh.Input{RefreshToken: test.token(&f)})
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if !errors.Is(err, errs.KindUnauthenticated) {
				t.Errorf("error = %v, want unauthenticated", err)
			}

			if code := errs.CodeOf(err); code != test.code {
				t.Errorf("code = %q, want %q", code, test.code)
			}
		})
	}
}

// recoveryCredential issues and stores one for the reader, and returns the value
// its holder would present.
func (f *fixture) recoveryCredential(t *testing.T) string {
	t.Helper()

	secret, err := f.auth.IssueRecovery(now())
	if err != nil {
		t.Fatalf("IssueRecovery: %v", err)
	}

	issued, err := credential.NewRecovery(f.phone.User.ID, secret.Digest, secret.ExpiresAt)
	if err != nil {
		t.Fatalf("NewRecovery: %v", err)
	}

	err = f.credentials.Create(t.Context(), issued)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	return secret.Value
}

// TestExecuteRefusesAnExpiredCredential is what bounds how long a device may
// stay away before it has to authenticate again.
func TestExecuteRefusesAnExpiredCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	// Past the thirty days the refresh lifetime defaults to.
	f.clock.Advance(31 * 24 * time.Hour)

	_, err := f.usecase.Execute(t.Context(), refresh.Input{RefreshToken: f.phone.Session.RefreshToken})
	if err == nil {
		t.Fatal("refreshing with an expired credential = nil, want an error")
	}

	if code := errs.CodeOf(err); code != refresh.CodeInvalidCredential {
		t.Errorf("code = %q, want %q", code, refresh.CodeInvalidCredential)
	}

	// And it must not have been spent: nothing was exchanged, so nothing was
	// consumed, and the reader has not been told which of their credentials
	// this was.
	stored, err := f.credentials.GetByTokenHash(t.Context(), f.auth.DigestOf(f.phone.Session.RefreshToken))
	if err != nil {
		t.Fatalf("the credential: %v", err)
	}

	if stored.Consumed {
		t.Error("an expired credential was consumed by the attempt to use it")
	}
}

// TestExecuteRefusesARevokedDevice is Quadro 17: an inactive device may not
// renew its credentials, which is the rule the flag exists for.
func TestExecuteRefusesARevokedDevice(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	f.phone.Device.Revoke()

	err := f.devices.Update(t.Context(), f.phone.Device)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	_, err = f.usecase.Execute(t.Context(), refresh.Input{RefreshToken: f.phone.Session.RefreshToken})
	if err == nil {
		t.Fatal("refreshing from a revoked device = nil, want an error")
	}

	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("error = %v, want permission denied", err)
	}

	if code := errs.CodeOf(err); code != refresh.CodeDeviceRevoked {
		t.Errorf("code = %q, want %q", code, refresh.CodeDeviceRevoked)
	}
}

// TestExecuteWithoutACredential is a malformed request rather than a failed
// exchange.
func TestExecuteWithoutACredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), refresh.Input{})
	if err == nil {
		t.Fatal("Execute presenting nothing = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != refresh.CodeNoCredential {
		t.Errorf("code = %q, want %q", code, refresh.CodeNoCredential)
	}
}
