package credential_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/credential"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestNewSession(t *testing.T) {
	t.Parallel()

	owner, appliance := uuid.New(), uuid.New()
	expires := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)

	issued, err := credential.NewSession(owner, appliance, "digest", expires)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	switch {
	case issued.Kind != credential.KindSessionRefresh:
		t.Errorf("Kind = %q, want %q", issued.Kind, credential.KindSessionRefresh)
	case issued.UserID != owner:
		t.Error("the credential does not name the reader it belongs to")
	case !issued.BelongsToDevice(appliance):
		t.Error("the credential does not name the device it was issued to, so revoking that device would miss it")
	case issued.Consumed:
		t.Error("a credential is issued unconsumed")
	}
}

// TestNewSessionWithoutADevice is the Go half of
// identity.credentials_session_needs_device: a session that named no device
// could not be revoked with that device, which is what unbinding one is for.
func TestNewSessionWithoutADevice(t *testing.T) {
	t.Parallel()

	_, err := credential.NewSession(uuid.New(), uuid.UUID{}, "digest", time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("NewSession without a device = nil, want an error")
	}

	assertInvalidArgument(t, err, credential.CodeInvalidCredential, "device_id")
}

// TestNewRecoveryNamesNoDevice covers the other half of UC08: the reader is
// recovering because they have lost access, possibly from an appliance that is
// not bound to the account at all.
func TestNewRecoveryNamesNoDevice(t *testing.T) {
	t.Parallel()

	issued, err := credential.NewRecovery(uuid.New(), "digest", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewRecovery: %v", err)
	}

	if issued.Kind != credential.KindPasswordRecovery {
		t.Errorf("Kind = %q, want %q", issued.Kind, credential.KindPasswordRecovery)
	}

	if issued.BelongsToDevice(uuid.UUID{}) {
		t.Error("a recovery credential reported that it belongs to the zero device")
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()

	expires := time.Now().Add(time.Hour)

	tests := []struct {
		name   string
		owner  uuid.UUID
		digest string
		expiry time.Time
		field  string
	}{
		{name: "no owner", owner: uuid.UUID{}, digest: "digest", expiry: expires, field: "user_id"},
		{name: "no digest", owner: uuid.New(), digest: "", expiry: expires, field: "token_hash"},
		{
			name: "a digest over the column", owner: uuid.New(), digest: strings.Repeat("a", 256),
			expiry: expires, field: "token_hash",
		},
		{name: "no expiry", owner: uuid.New(), digest: "digest", expiry: time.Time{}, field: "expires_at"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := credential.NewRecovery(test.owner, test.digest, test.expiry)
			if err == nil {
				t.Fatalf("NewRecovery = %+v, want an error", got)
			}

			assertInvalidArgument(t, err, credential.CodeInvalidCredential, test.field)
		})
	}
}

func TestUsable(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	expires := issuedAt.Add(time.Hour)

	issued, err := credential.NewRecovery(uuid.New(), "digest", expires)
	if err != nil {
		t.Fatalf("NewRecovery: %v", err)
	}

	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "while valid", at: issuedAt, want: true},
		{name: "the instant before expiry", at: expires.Add(-time.Nanosecond), want: true},
		// The boundary belongs to the expired side: a credential valid at the
		// instant it expires would be valid for one more comparison on a node
		// whose clock is a nanosecond behind.
		{name: "at the instant of expiry", at: expires, want: false},
		{name: "after expiry", at: expires.Add(time.Second), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := issued.Usable(test.at); got != test.want {
				t.Errorf("Usable(%s) = %t, want %t", test.at, got, test.want)
			}
		})
	}
}

// TestConsume is what makes a refresh a rotation: the credential presented is
// spent, and one that reappears afterwards was copied.
func TestConsume(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	issued, err := credential.NewSession(uuid.New(), uuid.New(), "digest", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if !issued.Usable(now) {
		t.Fatal("a fresh credential is not usable")
	}

	issued.Consume()

	if issued.Usable(now) {
		t.Error("a consumed credential is still usable, so presenting it twice would work")
	}
}

// TestRestoreKeepsTheState covers what a repository reads back: a credential
// already spent must not come back usable.
func TestRestoreKeepsTheState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	id := uuid.New()

	restored := credential.Restore(id, &credential.Props{
		UserID:    uuid.New(),
		DeviceID:  uuid.New(),
		Kind:      credential.KindSessionRefresh,
		TokenHash: "digest",
		ExpiresAt: now.Add(time.Hour),
		Consumed:  true,
	})

	if restored.ID != id {
		t.Error("Restore minted a new identifier over the one that was read")
	}

	if restored.Usable(now) {
		t.Error("a credential read back as consumed came back usable")
	}
}

func TestKindValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind credential.Kind
		want bool
	}{
		{kind: credential.KindSessionRefresh, want: true},
		{kind: credential.KindPasswordRecovery, want: true},
		{kind: "", want: false},
		{kind: "access", want: false},
	}

	for _, test := range tests {
		if got := test.kind.Valid(); got != test.want {
			t.Errorf("Kind(%q).Valid() = %t, want %t", test.kind, got, test.want)
		}
	}
}

// assertInvalidArgument checks that err is the kind, code and named field a
// client is expected to be able to act on.
func assertInvalidArgument(t *testing.T, err error, code, field string) {
	t.Helper()

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}

	if got := errs.CodeOf(err); got != code {
		t.Errorf("code = %q, want %q", got, code)
	}

	fields := errs.FieldsOf(err)
	if len(fields) == 0 {
		t.Fatalf("error %v names no field, so a client cannot point at the input", err)
	}

	if fields[0].Name != field {
		t.Errorf("field = %q, want %q", fields[0].Name, field)
	}

	if fields[0].Reason == "" {
		t.Error("the named field carries no reason")
	}
}
