package user

import (
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/persist/identitydb"
)

// identityMigration is the migration that declares the two indexes this
// repository names.
const identityMigration = "../../../../../migrations/000001_identity_and_federation.up.sql"

// TestConstraintNamesMatchTheMigration is what stops the error mapping from
// degrading silently.
//
// The two uniqueness rules of RN09 reach the driver as the same SQLSTATE, and
// only the constraint name tells them apart. Rename an index in a migration and
// nothing breaks: the repository stops recognizing the violation, answers with
// a generic "already exists", and a client that branched on the code to say
// which field to fix no longer can. The migration comment that renamed
// identity.credentials warns about exactly this; this test is that warning made
// mechanical.
func TestConstraintNamesMatchTheMigration(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(identityMigration)
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}

	for _, constraint := range []string{constraintIdentifier, constraintEmail} {
		if !strings.Contains(string(migration), constraint) {
			t.Errorf("the repository names the constraint %q, which %s does not declare",
				constraint, identityMigration)
		}
	}
}

func TestToDomain(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	id, origin := uuid.New(), uuid.New()

	email, passwordHash := "anthony@example.test", "$2a$12$digest"

	record := toDomain(&identitydb.IdentityUser{
		ID:             id,
		OriginServerID: origin,
		LocalName:      "anthony",
		DisplayName:    "Anthony Muller",
		Email:          &email,
		PasswordHash:   &passwordHash,
		CreatedAt:      created,
		UpdatedAt:      updated,
	})

	switch {
	case record.ID != id:
		t.Error("the row was rebuilt under a new identifier")
	case record.OriginServerID != origin:
		t.Error("the origin server was lost")
	case record.LocalName != "anthony" || record.DisplayName != "Anthony Muller":
		t.Errorf("the names were not carried across: %+v", record.Props)
	case record.Email != user.Email(email) || record.PasswordHash != passwordHash:
		t.Error("the credentials were not carried across")
	case !record.CreatedAt.Equal(created) || !record.UpdatedAt.Equal(updated):
		t.Error("the lifecycle instants were not carried across")
	case !record.Authenticates():
		t.Error("a row with a password reported that this node does not authenticate the reader")
	}
}

// TestToDomainOfAReplicatedReader is C03 read back: the row of a reader this
// node only replicates carries neither address nor password, and both columns
// are null.
func TestToDomainOfAReplicatedReader(t *testing.T) {
	t.Parallel()

	record := toDomain(&identitydb.IdentityUser{
		ID:             uuid.New(),
		OriginServerID: uuid.New(),
		LocalName:      "anthony",
		DisplayName:    "Anthony",
	})

	if !record.Email.IsZero() {
		t.Errorf("Email = %q, want it absent", record.Email)
	}

	if record.Authenticates() {
		t.Error("a row with a null password reported that this node authenticates the reader")
	}
}

func TestOptionalColumns(t *testing.T) {
	t.Parallel()

	if got := optionalEmail(""); got != nil {
		t.Errorf("optionalEmail of an absent address = %q, want NULL", *got)
	}

	got := optionalEmail("anthony@example.test")
	if got == nil || *got != "anthony@example.test" {
		t.Error("optionalEmail dropped an address that is present")
	}

	if got := optionalString(""); got != nil {
		t.Errorf("optionalString of an empty value = %q, want NULL", *got)
	}
}
