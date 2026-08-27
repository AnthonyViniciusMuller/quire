package register_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The node the readers of this file are registered on.
const testDomain user.ServerDomain = "quire-a.example"

// now is a fixed instant, so that the record's timestamps are decided by
// arithmetic rather than by how long the test took.
func now() time.Time { return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC) }

// fixture is the use case with its dependencies, and the doubles a test asserts
// against.
type fixture struct {
	usecase *register.Register
	users   *apptest.UserRepository
	hasher  *apptest.HashService
	server  *apptest.LocalServer
	clock   *apptest.Clock
}

func newFixture() fixture {
	users := apptest.NewUserRepository()
	hasher := apptest.NewHashService()
	server := apptest.NewLocalServer(testDomain)
	clock := apptest.NewClock(now())

	return fixture{
		usecase: register.New(users, hasher, server, clock),
		users:   users,
		hasher:  hasher,
		server:  server,
		clock:   clock,
	}
}

// valid is a request that should succeed.
func valid() register.Input {
	return register.Input{
		LocalName:   "Anthony",
		DisplayName: "  Anthony Muller ",
		Email:       "anthony@example.test",
		Password:    "correct horse battery staple",
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture()

	output, err := f.usecase.Execute(t.Context(), valid())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.LocalName != "anthony":
		t.Errorf("LocalName = %q, want it folded", output.User.LocalName)
	case output.User.DisplayName != "Anthony Muller":
		t.Errorf("DisplayName = %q, want it trimmed", output.User.DisplayName)
	case output.User.OriginServerID != f.server.ServerID:
		t.Error("the reader was not bound to the node the call was addressed to (UC14)")
	case !output.User.CreatedAt.Equal(now()):
		t.Errorf("CreatedAt = %s, want the instant the clock reported", output.User.CreatedAt)
	case !output.User.Authenticates():
		t.Error("the reader was stored without a password, so this node could not authenticate them")
	}

	// UC14 binds the reader to the node they registered with, and the
	// identifier is what says so (RN08, RN09).
	if want := "@anthony:quire-a.example"; output.FederatedID.String() != want {
		t.Errorf("FederatedID = %q, want %q", output.FederatedID, want)
	}

	// What was stored is the digest of the password and not the password: the
	// use case has to go through the hashing port rather than write the field
	// itself.
	if output.User.PasswordHash == valid().Password {
		t.Fatal("the password was stored as it was typed")
	}

	hashed, err := f.hasher.Verify(valid().Password, output.User.PasswordHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !hashed {
		t.Error("the stored digest is not the one the hashing port produces for that password")
	}

	stored, err := f.users.GetByLocalName(t.Context(), f.server.ServerID, "anthony")
	if err != nil {
		t.Fatalf("the reader was not written: %v", err)
	}

	if stored.ID != output.User.ID {
		t.Error("the reader that was written is not the one that was returned")
	}
}

// TestExecuteRejectsANameAlreadyTaken is the first half of RN09, and the point
// of the coded error: a client learns which of the two fields collided.
func TestExecuteRejectsANameAlreadyTaken(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if _, err := f.usecase.Execute(t.Context(), valid()); err != nil {
		t.Fatalf("the first registration: %v", err)
	}

	second := valid()
	second.Email = "anthony@other.test"

	_, err := f.usecase.Execute(t.Context(), second)
	if err == nil {
		t.Fatal("registering the same name twice = nil, want an error")
	}

	if !errors.Is(err, errs.KindAlreadyExists) {
		t.Errorf("error = %v, want already exists", err)
	}

	if code := errs.CodeOf(err); code != user.CodeLocalNameTaken {
		t.Errorf("code = %q, want %q", code, user.CodeLocalNameTaken)
	}

	if f.users.Count() != 1 {
		t.Errorf("the repository holds %d readers, want the second registration to have written nothing",
			f.users.Count())
	}
}

// TestExecuteRejectsAnAddressAlreadyRegistered is the second half, and the one
// that has to fold case: the index enforcing it is over lower(email).
func TestExecuteRejectsAnAddressAlreadyRegistered(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if _, err := f.usecase.Execute(t.Context(), valid()); err != nil {
		t.Fatalf("the first registration: %v", err)
	}

	second := valid()
	second.LocalName = "anthony2"
	second.Email = "ANTHONY@Example.test"

	_, err := f.usecase.Execute(t.Context(), second)
	if err == nil {
		t.Fatal("registering the same address in another capitalization = nil, want an error")
	}

	if code := errs.CodeOf(err); code != user.CodeEmailRegistered {
		t.Errorf("code = %q, want %q", code, user.CodeEmailRegistered)
	}
}

// TestExecuteAllowsTheSameNameOnAnotherServer is the rest of RN09: the
// identifier is unique on the pair, so @anthony on two nodes is two people.
func TestExecuteAllowsTheSameNameOnAnotherServer(t *testing.T) {
	t.Parallel()

	f := newFixture()

	if _, err := f.usecase.Execute(t.Context(), valid()); err != nil {
		t.Fatalf("the first registration: %v", err)
	}

	// The same node process, now answering for another domain, as a second
	// instance would.
	other := apptest.NewLocalServer("quire-b.example")
	elsewhere := register.New(f.users, f.hasher, other, f.clock)

	output, err := elsewhere.Execute(t.Context(), valid())
	if err != nil {
		t.Fatalf("registering the same name on another server: %v", err)
	}

	if want := "@anthony:quire-b.example"; output.FederatedID.String() != want {
		t.Errorf("FederatedID = %q, want %q", output.FederatedID, want)
	}
}

func TestExecuteRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(input *register.Input)
		code   string
		field  string
	}{
		{
			name:   "a name that is not an identifier",
			mutate: func(input *register.Input) { input.LocalName = "anthony@quire-a.example" },
			code:   user.CodeInvalidLocalName, field: "local_name",
		},
		{
			name:   "a blank display name",
			mutate: func(input *register.Input) { input.DisplayName = "   " },
			code:   user.CodeInvalidDisplayName, field: "display_name",
		},
		{
			name:   "an address that is not one",
			mutate: func(input *register.Input) { input.Email = "anthony" },
			code:   user.CodeInvalidEmail, field: "email",
		},
		{
			name:   "a password under the floor",
			mutate: func(input *register.Input) { input.Password = "short" },
			code:   user.CodeInvalidPassword, field: "password",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture()
			input := valid()
			test.mutate(&input)

			_, err := f.usecase.Execute(t.Context(), input)
			if err == nil {
				t.Fatal("Execute = nil, want an error")
			}

			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("error = %v, want an invalid argument", err)
			}

			if code := errs.CodeOf(err); code != test.code {
				t.Errorf("code = %q, want %q", code, test.code)
			}

			fields := errs.FieldsOf(err)
			if len(fields) == 0 || fields[0].Name != test.field {
				t.Errorf("fields = %v, want the one named %q", fields, test.field)
			}

			if f.users.Count() != 0 {
				t.Error("a rejected registration wrote a reader")
			}
		})
	}
}

// TestExecuteWithAnUnreachableCatalogue covers the node that cannot answer which
// node it is: no reader may be registered without an origin server (RN08).
func TestExecuteWithAnUnreachableCatalogue(t *testing.T) {
	t.Parallel()

	f := newFixture()
	f.server.Err = errs.New(errs.KindUnavailable, "the database is unavailable")

	if _, err := f.usecase.Execute(t.Context(), valid()); err == nil {
		t.Fatal("Execute without a catalogue = nil, want an error")
	} else if !errors.Is(err, errs.KindUnavailable) {
		t.Errorf("error = %v, want unavailable", err)
	}

	if f.users.Count() != 0 {
		t.Error("a reader was registered without an origin server")
	}
}
