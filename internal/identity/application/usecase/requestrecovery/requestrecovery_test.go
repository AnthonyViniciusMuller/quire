package requestrecovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/requestrecovery"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case with a reader already registered, since a recovery is
// only meaningful against an address somebody has.
type fixture struct {
	usecase     *requestrecovery.RequestRecovery
	credentials *apptest.CredentialRepository
	mailer      *apptest.Mailer
	reader      *user.User
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	users := apptest.NewUserRepository()
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
		Password:    "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	return fixture{
		usecase: requestrecovery.New(
			users, credentials, auth, mailer, server, clock, apptest.NewTransaction()),
		credentials: credentials,
		mailer:      mailer,
		reader:      registered.User,
	}
}

func TestExecuteSendsTheCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), requestrecovery.Input{Email: "anthony@example.test"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sent := f.mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d messages were sent, want 1", len(sent))
	}

	switch {
	case sent[0].Email != f.reader.Email:
		t.Errorf("the message went to %q, want the address on record", sent[0].Email)
	case sent[0].Token == "":
		t.Error("the message carries no credential, so the reader has nothing to present")
	case !sent[0].ExpiresAt.After(now()):
		t.Error("the credential is already expired when it is sent")
	}

	// What is stored is the digest and not the credential: the message is the
	// only copy of the value, which is why the node cannot send it twice.
	stored, err := f.credentials.GetByTokenHash(t.Context(), "digest:"+sent[0].Token)
	if err != nil {
		t.Fatalf("the credential was not stored: %v", err)
	}

	if stored.Kind != credential.KindPasswordRecovery {
		t.Errorf("Kind = %q, want %q", stored.Kind, credential.KindPasswordRecovery)
	}

	if stored.TokenHash == sent[0].Token {
		t.Error("the credential itself was stored")
	}
}

// TestExecuteAnswersTheSameForAnAddressNobodyHas is the whole shape of the call:
// an unauthenticated caller may ask about any address, so the reply must not say
// which ones are registered here.
func TestExecuteAnswersTheSameForAnAddressNobodyHas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
	}{
		{name: "an address nobody has", address: "nobody@example.test"},
		{name: "an address that is not one", address: "not an address"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			output, err := f.usecase.Execute(t.Context(), requestrecovery.Input{Email: test.address})
			if err != nil {
				t.Fatalf("Execute: %v, want the same reply a registered address gets", err)
			}

			if output != (requestrecovery.Output{}) {
				t.Error("the reply carries something, and it should carry nothing")
			}

			if sent := f.mailer.Sent(); len(sent) != 0 {
				t.Errorf("%d messages were sent for an address nobody has", len(sent))
			}
		})
	}
}

// TestExecuteEndsTheRecoveryAlreadyOutstanding keeps a reader who asked twice
// from leaving two live credentials behind.
func TestExecuteEndsTheRecoveryAlreadyOutstanding(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	input := requestrecovery.Input{Email: "anthony@example.test"}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("the first request: %v", err)
	}

	if _, err := f.usecase.Execute(t.Context(), input); err != nil {
		t.Fatalf("the second request: %v", err)
	}

	sent := f.mailer.Sent()
	if len(sent) != 2 {
		t.Fatalf("%d messages were sent, want 2", len(sent))
	}

	first, err := f.credentials.GetByTokenHash(t.Context(), "digest:"+sent[0].Token)
	if err != nil {
		t.Fatalf("the first credential: %v", err)
	}

	if first.Usable(now()) {
		t.Error("the first credential is still usable, so two messages can each set the password")
	}

	second, err := f.credentials.GetByTokenHash(t.Context(), "digest:"+sent[1].Token)
	if err != nil {
		t.Fatalf("the second credential: %v", err)
	}

	if !second.Usable(now()) {
		t.Error("the credential the reader is acting on is not usable")
	}
}

// TestExecuteHidesADeliveryFailure is what keeps the error path from undoing the
// uniformity of the reply: a delivery can only fail for an address that exists.
func TestExecuteHidesADeliveryFailure(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mailer.Err = errors.New("the transport is down")

	if _, err := f.usecase.Execute(t.Context(), requestrecovery.Input{Email: "anthony@example.test"}); err != nil {
		t.Errorf("Execute = %v, want the same reply an unknown address gets", err)
	}
}

// TestExecuteWithoutAnAddress is the one shape answered as a malformed request,
// because it says nothing about any account.
func TestExecuteWithoutAnAddress(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), requestrecovery.Input{})
	if err == nil {
		t.Fatal("Execute without an address = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != requestrecovery.CodeNoAddress {
		t.Errorf("code = %q, want %q", code, requestrecovery.CodeNoAddress)
	}
}
