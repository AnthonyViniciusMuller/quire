package login_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/login"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/device"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// testDomain is the node the readers of this file are registered on.
const testDomain user.ServerDomain = "quire-a.example"

// thePassword is the one every reader in this file was registered with.
const thePassword = "correct horse battery staple"

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case with its dependencies, and a reader already
// registered — since logging in is only meaningful against one.
type fixture struct {
	usecase     *login.Login
	users       *apptest.UserRepository
	devices     *apptest.DeviceRepository
	credentials *apptest.CredentialRepository
	server      *apptest.LocalServer
	transaction *apptest.Transaction
	reader      *user.User
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
	transaction := apptest.NewTransaction()

	registered, err := register.New(users, hasher, server, clock).Execute(t.Context(), register.Input{
		LocalName:   "anthony",
		DisplayName: "Anthony",
		Email:       "anthony@example.test",
		Password:    thePassword,
	})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	return fixture{
		usecase:     login.New(users, devices, credentials, hasher, auth, server, clock, transaction),
		users:       users,
		devices:     devices,
		credentials: credentials,
		server:      server,
		transaction: transaction,
		reader:      registered.User,
	}
}

// firstLogin is a request from an appliance that has never been bound.
func firstLogin() login.Input {
	return login.Input{
		LocalName: "anthony",
		Password:  thePassword,
		Device:    login.Binding{Name: "Pixel 9", Platform: "android"},
	}
}

func TestExecuteBindsTheDeviceAndIssuesTheSession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), firstLogin())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.ID != f.reader.ID:
		t.Error("the session was issued to another reader")
	case output.Device == nil || output.Device.ID == (uuid.UUID{}):
		t.Fatal("no device was bound, so the reply carries no identifier for its vector clocks")
	case !output.Device.Active:
		t.Error("the device was bound inactive")
	case output.Device.Name != "Pixel 9" || output.Device.Platform != "android":
		t.Errorf("the device was bound as %+v", output.Device.Props)
	case output.Session.AccessToken == "":
		t.Error("no access token was issued")
	case output.Session.RefreshToken == "":
		t.Error("no refresh credential was issued")
	}

	// RNF11 asks for a short-lived access token, so the refresh credential has
	// to outlive it or a device would have nothing to refresh with.
	if !output.Session.AccessTokenExpiresAt.Before(output.Session.RefreshTokenExpiresAt) {
		t.Error("the access token outlives the credential meant to replace it")
	}

	// The credential the device now holds must be stored as a digest and not as
	// itself.
	stored, err := f.credentials.GetByTokenHash(t.Context(), "digest:"+output.Session.RefreshToken)
	if err != nil {
		t.Fatalf("the refresh credential was not stored: %v", err)
	}

	switch {
	case stored.TokenHash == output.Session.RefreshToken:
		t.Error("the credential itself was stored, so a dump of the table could be replayed")
	case !stored.BelongsToDevice(output.Device.ID):
		t.Error("the credential does not name the device, so revoking that device would miss it")
	case stored.Kind != credential.KindSessionRefresh:
		t.Errorf("Kind = %q, want %q", stored.Kind, credential.KindSessionRefresh)
	}

	// Binding the device and issuing its credential are one unit of work: a
	// device bound without one is an appliance attached to an account that
	// cannot use it.
	if calls := f.transaction.Calls(); calls != 1 {
		t.Errorf("the work ran in %d units, want exactly 1", calls)
	}
}

// TestExecuteReusesABoundDevice is what keeps one appliance to one clock entry:
// a device that presents its identifier goes on being the same device.
func TestExecuteReusesABoundDevice(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	first, err := f.usecase.Execute(t.Context(), firstLogin())
	if err != nil {
		t.Fatalf("the first login: %v", err)
	}

	second := firstLogin()
	second.Device = login.Binding{DeviceID: first.Device.ID.String()}

	again, err := f.usecase.Execute(t.Context(), second)
	if err != nil {
		t.Fatalf("the second login: %v", err)
	}

	if again.Device.ID != first.Device.ID {
		t.Error("logging in again minted a second identifier, which would start a clock entry that never merges")
	}

	if f.devices.Count() != 1 {
		t.Errorf("the repository holds %d devices, want 1", f.devices.Count())
	}

	// Two sessions at once is RN07: a reader uses several appliances, and a
	// second login must not invalidate the first.
	if again.Session.RefreshToken == first.Session.RefreshToken {
		t.Error("the second login reissued the first credential")
	}

	if live := f.credentials.Live(credential.KindSessionRefresh, now()); live != 2 {
		t.Errorf("%d credentials are live, want both sessions to be (RN07)", live)
	}
}

// TestExecuteIgnoresTheNameOfABoundDevice keeps a login from quietly rewriting
// a record the reader uses to recognize their own appliances.
func TestExecuteIgnoresTheNameOfABoundDevice(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	first, err := f.usecase.Execute(t.Context(), firstLogin())
	if err != nil {
		t.Fatalf("the first login: %v", err)
	}

	renamed := firstLogin()
	renamed.Device = login.Binding{DeviceID: first.Device.ID.String(), Name: "Something else", Platform: "windows"}

	again, err := f.usecase.Execute(t.Context(), renamed)
	if err != nil {
		t.Fatalf("the second login: %v", err)
	}

	if again.Device.Name != "Pixel 9" || again.Device.Platform != "android" {
		t.Errorf("the device came back as %+v, want the record it was bound with", again.Device.Props)
	}
}

func TestExecuteAcceptsTheAddressAsWell(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := firstLogin()
	input.LocalName = ""
	input.Email = "ANTHONY@Example.test"

	output, err := f.usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute by address: %v", err)
	}

	if output.User.ID != f.reader.ID {
		t.Error("the address matched another reader")
	}
}

// TestExecuteAnswersEveryBadCredentialAlike is the enumeration defence: a name
// nobody has, a name that could never exist, and the right name with the wrong
// password are one answer.
func TestExecuteAnswersEveryBadCredentialAlike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(input *login.Input)
	}{
		{
			name:   "a reader who is not registered here",
			mutate: func(input *login.Input) { input.LocalName = "somebody" },
		},
		{
			name:   "a name that could never be registered",
			mutate: func(input *login.Input) { input.LocalName = "Not A Name!" },
		},
		{
			name:   "an address nobody has",
			mutate: func(input *login.Input) { input.LocalName = ""; input.Email = "nobody@example.test" },
		},
		{
			name:   "the right reader and the wrong password",
			mutate: func(input *login.Input) { input.Password = "not the password" },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			input := firstLogin()
			test.mutate(&input)

			_, err := f.usecase.Execute(t.Context(), input)
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if !errors.Is(err, errs.KindUnauthenticated) {
				t.Errorf("error = %v, want unauthenticated", err)
			}

			if code := errs.CodeOf(err); code != login.CodeInvalidCredentials {
				t.Errorf("code = %q, want %q: every one of these is the same fact to whoever asked",
					code, login.CodeInvalidCredentials)
			}

			if f.devices.Count() != 0 {
				t.Error("a refused login bound a device")
			}
		})
	}
}

// TestExecuteWithoutANamedReader is the one shape that is answered differently,
// because it is a malformed request rather than a failed authentication.
func TestExecuteWithoutANamedReader(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := firstLogin()
	input.LocalName = ""

	_, err := f.usecase.Execute(t.Context(), input)
	if err == nil {
		t.Fatal("Execute naming nobody = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != login.CodeNoReaderNamed {
		t.Errorf("code = %q, want %q", code, login.CodeNoReaderNamed)
	}
}

// TestExecuteRefusesAnotherReadersDevice covers what a reply must not be: an
// oracle for which identifiers belong to somebody else. It is answered exactly
// as an identifier nobody has.
func TestExecuteRefusesAnotherReadersDevice(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	stranger, err := device.New(uuid.New(), "Someone else's tablet", "android")
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}

	if err := f.devices.Create(t.Context(), stranger); err != nil {
		t.Fatalf("Create: %v", err)
	}

	tests := []struct {
		name     string
		deviceID string
	}{
		{name: "another reader's device", deviceID: stranger.ID.String()},
		{name: "an identifier nobody has", deviceID: uuid.New().String()},
		{name: "a value that is not an identifier", deviceID: "not-a-uuid"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := firstLogin()
			input.Device = login.Binding{DeviceID: test.deviceID}

			_, err := f.usecase.Execute(t.Context(), input)
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if code := errs.CodeOf(err); code != login.CodeUnknownDevice {
				t.Errorf("code = %q, want %q for all three", code, login.CodeUnknownDevice)
			}
		})
	}
}

// TestExecuteRefusesARevokedDevice is Quadro 17 read literally: an inactive
// device may not renew its credentials, and issuing it a session is renewing
// them.
func TestExecuteRefusesARevokedDevice(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	first, err := f.usecase.Execute(t.Context(), firstLogin())
	if err != nil {
		t.Fatalf("the first login: %v", err)
	}

	first.Device.Revoke()

	err = f.devices.Update(t.Context(), first.Device)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	input := firstLogin()
	input.Device = login.Binding{DeviceID: first.Device.ID.String()}

	_, err = f.usecase.Execute(t.Context(), input)
	if err == nil {
		t.Fatal("logging in from a revoked device = nil, want an error")
	}

	if !errors.Is(err, errs.KindPermissionDenied) {
		t.Errorf("error = %v, want permission denied", err)
	}

	if code := errs.CodeOf(err); code != login.CodeDeviceRevoked {
		t.Errorf("code = %q, want %q", code, login.CodeDeviceRevoked)
	}
}

// TestExecuteRejectsAnUnusableBinding covers the appliance being bound for the
// first time: it has to describe itself.
func TestExecuteRejectsAnUnusableBinding(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := firstLogin()
	input.Device = login.Binding{Name: "   ", Platform: "android"}

	_, err := f.usecase.Execute(t.Context(), input)
	if err == nil {
		t.Fatal("binding a device with a blank name = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if f.devices.Count() != 0 {
		t.Error("a refused binding wrote a device")
	}
}
