package user_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// registered is a reader as UC14 leaves them, for the tests that need one.
func registered(t *testing.T, now time.Time) *user.User {
	t.Helper()

	record, err := user.New(&user.Props{
		OriginServerID: uuid.New(),
		LocalName:      "anthony",
		DisplayName:    "Anthony",
		Email:          "anthony@example.test",
		PasswordHash:   "$2a$12$first",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("user.New: %v", err)
	}

	return record
}

func TestNew(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	origin := uuid.New()

	record, err := user.New(&user.Props{
		OriginServerID: origin,
		LocalName:      "anthony",
		DisplayName:    "Anthony Muller",
		Email:          "anthony@example.test",
		PasswordHash:   "$2a$12$digest",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("user.New: %v", err)
	}

	switch {
	case record.ID == (uuid.UUID{}):
		t.Error("the reader was given no identifier, so the first device has nothing to reference")
	case record.OriginServerID != origin:
		t.Error("the reader was not bound to the node that registered them")
	case !record.CreatedAt.Equal(now) || !record.UpdatedAt.Equal(now):
		t.Error("the lifecycle timestamps were not both stamped with the instant supplied")
	case !record.Authenticates():
		t.Error("a reader registered here carries a password, so this node authenticates them")
	}

	id, err := record.FederatedID("quire-a.example")
	if err != nil {
		t.Fatalf("FederatedID: %v", err)
	}

	if want := "@anthony:quire-a.example"; id.String() != want {
		t.Errorf("FederatedID = %q, want %q", id, want)
	}
}

// TestNewIdentifiersAreDistinct is what makes the identifier usable as a key:
// two readers registered with the same name on different nodes must not share
// one.
func TestNewIdentifiersAreDistinct(t *testing.T) {
	t.Parallel()

	now := time.Now()

	if first, second := registered(t, now), registered(t, now); first.ID == second.ID {
		t.Error("two readers were minted the same identifier")
	}
}

// TestNewValidatesEvenTypedValues covers the belt and braces: a value object can
// be built by conversion as well as by its Parse function, so the constructor
// checks again and an entity that exists is an entity that is valid.
func TestNewRejects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	valid := user.Props{
		OriginServerID: uuid.New(),
		LocalName:      "anthony",
		DisplayName:    "Anthony",
		Email:          "anthony@example.test",
		PasswordHash:   "$2a$12$digest",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	tests := []struct {
		name   string
		mutate func(props *user.Props)
		code   string
		field  string
	}{
		{
			name:   "a local name that was converted rather than parsed",
			mutate: func(props *user.Props) { props.LocalName = "Anthony" },
			code:   user.CodeInvalidLocalName, field: "local_name",
		},
		{
			name:   "a blank display name",
			mutate: func(props *user.Props) { props.DisplayName = "" },
			code:   user.CodeInvalidDisplayName, field: "display_name",
		},
		{
			name:   "an address that is not one",
			mutate: func(props *user.Props) { props.Email = "anthony" },
			code:   user.CodeInvalidEmail, field: "email",
		},
		{
			// RN08 at the one place it can be checked without a database.
			name:   "no origin server",
			mutate: func(props *user.Props) { props.OriginServerID = uuid.UUID{} },
			code:   user.CodeInvalidUser, field: "origin_server_id",
		},
		{
			name:   "no password",
			mutate: func(props *user.Props) { props.PasswordHash = "" },
			code:   user.CodeInvalidUser, field: "password_hash",
		},
		{
			name:   "no instant",
			mutate: func(props *user.Props) { props.CreatedAt = time.Time{} },
			code:   user.CodeInvalidUser, field: "created_at",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			props := valid
			test.mutate(&props)

			got, err := user.New(&props)
			if err == nil {
				t.Fatalf("user.New = %+v, want an error", got)
			}

			assertInvalidArgument(t, err, test.code, test.field)
		})
	}
}

// TestRestoreKeepsTheIdentifier covers what a repository needs: a row read back
// is the same reader, not a new one.
func TestRestoreKeepsTheIdentifier(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	id := uuid.New()

	restored := user.Restore(id, &user.Props{
		OriginServerID: uuid.New(),
		LocalName:      "anthony",
		DisplayName:    "Anthony",
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	if restored.ID != id {
		t.Error("Restore minted a new identifier over the one that was read")
	}

	// C03: a reader this node only replicates carries neither an address nor a
	// password, and the absence of the password is what says so.
	if restored.Authenticates() {
		t.Error("a row without a password reported that this node authenticates the reader")
	}

	if !restored.Email.IsZero() {
		t.Error("a replicated reader must not carry an address (RN09)")
	}
}

func TestUpdates(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	changed := created.Add(time.Hour)
	record := registered(t, created)

	if err := record.Rename("Anthony M.", changed); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if err := record.ChangeEmail("anthony@other.test", changed); err != nil {
		t.Fatalf("ChangeEmail: %v", err)
	}

	record.ChangePassword("$2a$12$second", changed)

	switch {
	case record.DisplayName != "Anthony M.":
		t.Errorf("DisplayName = %q, want the new one", record.DisplayName)
	case record.Email != "anthony@other.test":
		t.Errorf("Email = %q, want the new one", record.Email)
	case record.PasswordHash != "$2a$12$second":
		t.Error("the password digest was not replaced")
	case !record.UpdatedAt.Equal(changed):
		t.Errorf("UpdatedAt = %s, want %s", record.UpdatedAt, changed)
	case !record.CreatedAt.Equal(created):
		t.Error("the creation instant moved")
	}
}

// TestRejectedUpdateChangesNothing matters for a client that retries: after
// fixing one field it must not find another already applied.
func TestRejectedUpdateChangesNothing(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	record := registered(t, created)

	err := record.Rename("", created.Add(time.Hour))
	if err == nil {
		t.Fatal("Rename to a blank name = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Rename = %v, want an invalid argument", err)
	}

	if record.DisplayName != "Anthony" {
		t.Errorf("DisplayName = %q after a rejected rename, want it unchanged", record.DisplayName)
	}

	if !record.UpdatedAt.Equal(created) {
		t.Error("a rejected rename moved the instant of the last change")
	}
}
