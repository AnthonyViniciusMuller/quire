package changeemail_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/changeemail"
	"github.com/anthonyvsmuller/quire/internal/identity/application/usecase/register"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func now() time.Time { return time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC) }

// thePassword is what the reader registered with, and what this call demands
// back before it will touch the address.
const thePassword = "correct horse battery staple"

// fixture is the use case, the reader whose address it changes, the transport
// the notice goes through, and a second reader whose address is already taken.
type fixture struct {
	usecase *changeemail.ChangeEmail
	users   *apptest.UserRepository
	mailer  *apptest.Mailer
	clock   *apptest.Clock
	reader  *user.User
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	users := apptest.NewUserRepository()
	server := apptest.NewLocalServer("quire-a.example")
	clock := apptest.NewClock(now())
	hasher := apptest.NewHashService()
	mailer := apptest.NewMailer()
	registrar := register.New(users, hasher, server, clock)

	output, err := registrar.Execute(t.Context(), register.Input{
		LocalName:   "anthony",
		DisplayName: "Anthony",
		Email:       "anthony@example.test",
		Password:    thePassword,
	})
	if err != nil {
		t.Fatalf("registering the reader: %v", err)
	}

	if _, err := registrar.Execute(t.Context(), register.Input{
		LocalName:   "somebody",
		DisplayName: "Somebody",
		Email:       "taken@example.test",
		Password:    thePassword,
	}); err != nil {
		t.Fatalf("registering the second reader: %v", err)
	}

	return fixture{
		usecase: changeemail.New(users, hasher, mailer, server, clock),
		users:   users,
		mailer:  mailer,
		clock:   clock,
		reader:  output.User,
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.clock.Advance(time.Hour)

	output, err := f.usecase.Execute(t.Context(), changeemail.Input{
		UserID:   f.reader.ID,
		Password: thePassword,
		Email:    "anthony@other.test",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.User.Email != "anthony@other.test":
		t.Errorf("Email = %q, want the new one", output.User.Email)
	case output.User.LocalName != f.reader.LocalName:
		t.Error("the local name changed, and it is half the identifier RN09 makes unique")
	case !output.User.UpdatedAt.After(f.reader.UpdatedAt):
		t.Error("the instant of the last change did not move")
	case output.FederatedID.String() != "@anthony:quire-a.example":
		t.Errorf("FederatedID = %q, want it unchanged", output.FederatedID)
	}
}

// The half of C14 that survives a compromise. The password check stops a device
// left unlocked for a minute; it does not stop somebody who learned the
// password, and for them this notice is how the reader finds out at all.
func TestExecuteTellsThePreviousAddress(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.clock.Advance(time.Hour)

	if _, err := f.usecase.Execute(t.Context(), changeemail.Input{
		UserID:   f.reader.ID,
		Password: thePassword,
		Email:    "anthony@other.test",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	notices := f.mailer.Notices()
	if len(notices) != 1 {
		t.Fatalf("%d notices were sent, want exactly one", len(notices))
	}

	notice := notices[0]

	switch {
	case notice.PreviousEmail != "anthony@example.test":
		t.Errorf("PreviousEmail = %q, want the address that is being replaced", notice.PreviousEmail)
	case notice.NewEmail != "anthony@other.test":
		t.Errorf("NewEmail = %q, want the address it was changed to", notice.NewEmail)
	case notice.DisplayName != f.reader.DisplayName:
		t.Errorf("DisplayName = %q, want the reader's", notice.DisplayName)
	case !notice.ChangedAt.Equal(f.clock.Now()):
		t.Errorf("ChangedAt = %s, want the instant of the change", notice.ChangedAt)
	}
}

// The whole of why this call is not a field of UpdateUser: a session proves a
// device is unlocked, not that the reader is at it.
func TestExecuteRefusesTheWrongPassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), changeemail.Input{
		UserID:   f.reader.ID,
		Password: "not the password",
		Email:    "attacker@example.test",
	})
	if err == nil {
		t.Fatal("Execute with the wrong password = nil, want an error")
	}

	if !errors.Is(err, errs.KindUnauthenticated) {
		t.Errorf("error = %v, want unauthenticated", err)
	}

	if code := errs.CodeOf(err); code != changeemail.CodeWrongPassword {
		t.Errorf("code = %q, want %q", code, changeemail.CodeWrongPassword)
	}

	stored, err := f.users.GetByID(t.Context(), f.reader.ID)
	if err != nil {
		t.Fatalf("the reader: %v", err)
	}

	if stored.Email != f.reader.Email {
		t.Errorf("Email = %q, want the record unchanged", stored.Email)
	}

	if notices := f.mailer.Notices(); len(notices) != 0 {
		t.Errorf("%d notices were sent for a refused change", len(notices))
	}
}

// The address is parsed before the password is verified, so a malformed request
// costs no hashing — and a caller guessing passwords learns nothing from how
// long one took.
func TestExecuteRefusesAnAddressThatIsNotOne(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), changeemail.Input{
		UserID:   f.reader.ID,
		Password: thePassword,
		Email:    "not an address",
	})
	if err == nil {
		t.Fatal("Execute with an address that is not one = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("error = %v, want an invalid argument", err)
	}
}

// RN09 on this path: the address is unique within the origin server however it
// got there.
func TestExecuteRefusesAnAddressAlreadyRegistered(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(), changeemail.Input{
		UserID:   f.reader.ID,
		Password: thePassword,
		Email:    "TAKEN@example.test",
	})
	if err == nil {
		t.Fatal("taking another reader's address = nil, want an error")
	}

	if !errors.Is(err, errs.KindAlreadyExists) {
		t.Errorf("error = %v, want already exists", err)
	}

	if code := errs.CodeOf(err); code != user.CodeEmailRegistered {
		t.Errorf("code = %q, want %q", code, user.CodeEmailRegistered)
	}

	if notices := f.mailer.Notices(); len(notices) != 0 {
		t.Errorf("%d notices were sent for a refused change", len(notices))
	}
}

// The address has already changed by the time the notice is attempted, so a
// transport that is down must not turn a write that succeeded into a call that
// failed — the reader would be told their address is still the old one, which
// is worse than a notice that did not arrive.
func TestExecuteSucceedsWhenTheNoticeCannotBeDelivered(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mailer.Err = errors.New("the relay is down")

	output, err := f.usecase.Execute(t.Context(), changeemail.Input{
		UserID:   f.reader.ID,
		Password: thePassword,
		Email:    "anthony@other.test",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.User.Email != "anthony@other.test" {
		t.Errorf("Email = %q, want the change to have happened anyway", output.User.Email)
	}

	stored, err := f.users.GetByID(t.Context(), f.reader.ID)
	if err != nil {
		t.Fatalf("the reader: %v", err)
	}

	if stored.Email != "anthony@other.test" {
		t.Errorf("the stored address is %q, want the new one", stored.Email)
	}
}
