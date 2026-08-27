package user_test

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// schemaLocalNamePattern is the CHECK constraint identity.users carries, copied
// verbatim from 000001_identity_and_federation.up.sql.
const schemaLocalNamePattern = `^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`

func TestParseLocalName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  user.LocalName
	}{
		{name: "one character", input: "a", want: "a"},
		{name: "digits", input: "42", want: "42"},
		{name: "separators inside", input: "an.tho-ny_1", want: "an.tho-ny_1"},
		{name: "folded to lower case", input: "Anthony", want: "anthony"},
		{name: "surrounding space removed", input: "  anthony\t", want: "anthony"},
		{
			name:  "at the width of the column",
			input: strings.Repeat("a", 64),
			want:  user.LocalName(strings.Repeat("a", 64)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := user.ParseLocalName(test.input)
			if err != nil {
				t.Fatalf("ParseLocalName(%q): %v", test.input, err)
			}

			if got != test.want {
				t.Errorf("ParseLocalName(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseLocalNameRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "only space", input: "   "},
		{name: "leading separator", input: ".anthony"},
		{name: "trailing separator", input: "anthony-"},
		{name: "an at sign", input: "anthony@quire-a.example"},
		{name: "a colon", input: "anthony:quire-a.example"},
		{name: "a slash", input: "anthony/1"},
		{name: "a space inside", input: "an thony"},
		{name: "not ascii", input: "antônio"},
		{name: "one over the width of the column", input: strings.Repeat("a", 65)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := user.ParseLocalName(test.input)
			if err == nil {
				t.Fatalf("ParseLocalName(%q) = %q, want an error", test.input, got)
			}

			assertInvalidArgument(t, err, user.CodeInvalidLocalName, "local_name")
		})
	}
}

// TestParseLocalNameMatchesTheSchema is the point of writing the rule out by
// hand rather than compiling it: the loop has to accept exactly what the CHECK
// constraint accepts.
//
// Stricter would mean rejecting a name the database would have taken, which is
// merely surprising. Laxer is the one that matters: the value would reach
// PostgreSQL, come back as a constraint violation naming a table and a column,
// and be answered with an internal error instead of a named field.
func TestParseLocalNameMatchesTheSchema(t *testing.T) {
	t.Parallel()

	schema := regexp.MustCompile(schemaLocalNamePattern)

	for _, candidate := range localNameCorpus() {
		_, err := user.ParseLocalName(candidate)

		// Against the folded and trimmed value, since that is what would reach
		// the column: ParseLocalName normalizes first and the CHECK only ever
		// sees the result.
		normalized := strings.ToLower(strings.TrimSpace(candidate))

		accepted := err == nil
		if want := schema.MatchString(normalized); accepted != want {
			t.Errorf("ParseLocalName(%q) accepted = %t, the schema accepts %q = %t",
				candidate, accepted, normalized, want)
		}
	}
}

// localNameCorpus is every string of up to three characters over an alphabet
// holding one of each class the rule distinguishes, plus the lengths at the
// boundary of the column.
//
// Exhaustive over the short strings is what makes the comparison meaningful:
// the rule is about the first and last character, so every case it can
// distinguish appears in a string of three or fewer.
func localNameCorpus() []string {
	alphabet := []string{"a", "z", "0", ".", "-", "_", "A", "@", ":", " ", "é"}

	corpus := make([]string, 0, 4+len(alphabet)*(1+len(alphabet)*(1+len(alphabet))))
	corpus = append(corpus, "", strings.Repeat("a", 64), strings.Repeat("a", 65), "a"+strings.Repeat(".", 62)+"a")

	for _, first := range alphabet {
		corpus = append(corpus, first)

		for _, second := range alphabet {
			corpus = append(corpus, first+second)

			for _, third := range alphabet {
				corpus = append(corpus, first+second+third)
			}
		}
	}

	return corpus
}

func TestParseEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  user.Email
	}{
		{name: "plain", input: "anthony@example.test", want: "anthony@example.test"},
		{name: "surrounding space removed", input: " anthony@example.test ", want: "anthony@example.test"},
		{name: "capitalization kept", input: "Anthony@Example.test", want: "Anthony@Example.test"},
		{name: "a plus address", input: "anthony+quire@example.test", want: "anthony+quire@example.test"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := user.ParseEmail(test.input)
			if err != nil {
				t.Fatalf("ParseEmail(%q): %v", test.input, err)
			}

			if got != test.want {
				t.Errorf("ParseEmail(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseEmailRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "no at sign", input: "anthony.example.test"},
		{name: "nothing before the at sign", input: "@example.test"},
		{name: "nothing after the at sign", input: "anthony@"},
		{name: "two at signs", input: "anthony@example@test"},
		{name: "a space inside", input: "anthony @example.test"},
		{name: "longer than the column", input: strings.Repeat("a", 250) + "@example.test"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := user.ParseEmail(test.input)
			if err == nil {
				t.Fatalf("ParseEmail(%q) = %q, want an error", test.input, got)
			}

			assertInvalidArgument(t, err, user.CodeInvalidEmail, "email")
		})
	}
}

// TestEmailFoldsForUniqueness covers what RN09 makes unique: the address folded
// to lower case, which is what the index on lower(email) compares.
func TestEmailFoldsForUniqueness(t *testing.T) {
	t.Parallel()

	first, err := user.ParseEmail("Anthony@Example.test")
	if err != nil {
		t.Fatalf("ParseEmail: %v", err)
	}

	second, err := user.ParseEmail("anthony@example.test")
	if err != nil {
		t.Fatalf("ParseEmail: %v", err)
	}

	if first == second {
		t.Error("the stored addresses are equal, so the reader's own capitalization was lost")
	}

	if first.Fold() != second.Fold() {
		t.Errorf("Fold() = %q and %q, want them equal: the index compares lower(email)", first.Fold(), second.Fold())
	}
}

func TestParseDisplayName(t *testing.T) {
	t.Parallel()

	got, err := user.ParseDisplayName("  Anthony Muller ")
	if err != nil {
		t.Fatalf("ParseDisplayName: %v", err)
	}

	if want := user.DisplayName("Anthony Muller"); got != want {
		t.Errorf("ParseDisplayName = %q, want %q", got, want)
	}

	// Characters, not bytes: PostgreSQL counts a varchar the same way, so a
	// name of accented characters that fits the column must be accepted.
	if _, err := user.ParseDisplayName(strings.Repeat("é", 120)); err != nil {
		t.Errorf("ParseDisplayName of 120 accented characters: %v", err)
	}

	for _, input := range []string{"", "   ", strings.Repeat("a", 121)} {
		_, err := user.ParseDisplayName(input)
		if err == nil {
			t.Errorf("ParseDisplayName(%q) = nil, want an error", input)

			continue
		}

		assertInvalidArgument(t, err, user.CodeInvalidDisplayName, "display_name")
	}
}

func TestFederatedID(t *testing.T) {
	t.Parallel()

	id, err := user.ParseFederatedID("@anthony:quire-a.example")
	if err != nil {
		t.Fatalf("ParseFederatedID: %v", err)
	}

	if id.LocalName != "anthony" || id.Domain != "quire-a.example" {
		t.Errorf("ParseFederatedID = %+v, want the two halves apart", id)
	}

	if want := "@anthony:quire-a.example"; id.String() != want {
		t.Errorf("String() = %q, want %q", id.String(), want)
	}

	// A domain carrying a port is what the two-node development federation
	// looks like, and the colon in it must not confuse the split.
	withPort, err := user.ParseFederatedID("@anthony:localhost:19090")
	if err != nil {
		t.Fatalf("ParseFederatedID with a port: %v", err)
	}

	if withPort.Domain != "localhost:19090" {
		t.Errorf("Domain = %q, want the whole authority including the port", withPort.Domain)
	}
}

func TestFederatedIDRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "no at sign", input: "anthony:quire-a.example"},
		{name: "no server", input: "@anthony"},
		{name: "no local name", input: "@:quire-a.example"},
		{name: "no domain", input: "@anthony:"},
		{name: "a scheme", input: "@anthony:https://quire-a.example"},
		{name: "a path", input: "@anthony:quire-a.example/v1"},
		{name: "a port that is not a number", input: "@anthony:quire-a.example:grpc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := user.ParseFederatedID(test.input)
			if err == nil {
				t.Fatalf("ParseFederatedID(%q) = %v, want an error", test.input, got)
			}

			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("ParseFederatedID(%q) = %v, want an invalid argument", test.input, err)
			}
		})
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

func TestPasswordValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password user.Password
		valid    bool
	}{
		{name: "at the floor", password: "12345678", valid: true},
		{name: "a passphrase", password: "correct horse battery staple", valid: true},
		{
			// NIST SP 800-63B withdrew the composition rules, so a password of
			// one character class is acceptable if it is long enough.
			name: "no composition rule", password: "aaaaaaaaaaaaaaaa", valid: true,
		},
		{name: "at the ceiling", password: user.Password(strings.Repeat("a", 72)), valid: true},
		{name: "empty", password: "", valid: false},
		{name: "one under the floor", password: "1234567", valid: false},
		{name: "one byte over the ceiling", password: user.Password(strings.Repeat("a", 73)), valid: false},
		{
			// The ceiling is bcrypt's, and bcrypt counts bytes: twenty-five
			// accented characters are fifty bytes and fit, thirty-seven are
			// seventy-four and do not.
			name:     "accented characters past the ceiling in bytes",
			password: user.Password(strings.Repeat("é", 37)), valid: false,
		},
		{
			name:     "accented characters inside the ceiling",
			password: user.Password(strings.Repeat("é", 25)), valid: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.password.Validate()

			switch {
			case test.valid && err != nil:
				t.Errorf("Validate = %v, want it accepted", err)
			case !test.valid && err == nil:
				t.Error("Validate = nil, want it rejected")
			case !test.valid:
				assertInvalidArgument(t, err, user.CodeInvalidPassword, "password")
			}
		})
	}
}

// TestPasswordDoesNotRenderItself is what keeps the plaintext out of a log line
// or an error message, where every other type in this package invites
// formatting.
func TestPasswordDoesNotRenderItself(t *testing.T) {
	t.Parallel()

	secret := user.Password("correct horse battery staple")

	if rendered := secret.String(); strings.Contains(rendered, "correct") {
		t.Errorf("String() = %q, want the password hidden", rendered)
	}

	// Through the verb, as a log line or a wrapped error would reach it.
	if formatted := fmt.Sprintf("password=%v", secret); strings.Contains(formatted, "correct") {
		t.Errorf("formatted as %q, want the password hidden", formatted)
	}
}
