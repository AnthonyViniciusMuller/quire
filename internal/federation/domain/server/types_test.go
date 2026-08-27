package server_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// TestParseDomainFolds covers the reason the column is read and written in one
// case: a reader who types Quire-A.Example means the node one who types it in
// lower case means, and two rows for one node would be two pins for one key.
func TestParseDomainFolds(t *testing.T) {
	t.Parallel()

	if got := server.ParseDomain("  Quire-A.Example  "); got != "quire-a.example" {
		t.Errorf("ParseDomain = %q, want %q", got, "quire-a.example")
	}
}

func TestDomainValidate(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		domain server.Domain
		valid  bool
	}{
		"a host":                    {"quire-a.example", true},
		"a host with a port":        {"quire-a.example:9090", true},
		"a single label":            {"localhost", true},
		"empty":                     {"", false},
		"upper case":                {"Quire-A.example", false},
		"a scheme":                  {"https://quire-a.example", false},
		"a path":                    {"quire-a.example/nodes", false},
		"a leading dot":             {".quire-a.example", false},
		"a trailing hyphen":         {"quire-a.example-", false},
		"a port that is not digits": {"quire-a.example:grpc", false},
		"a port of six digits":      {"quire-a.example:123456", false},
		"an at sign from an id":     {"@anthony:quire-a.example", false},
		"longer than the column":    {server.Domain(strings.Repeat("a", 256)), false},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := testCase.domain.Validate()
			if testCase.valid {
				if err != nil {
					t.Fatalf("Validate(%q) = %v, want nil", testCase.domain, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate(%q) = nil, want an error", testCase.domain)
			}

			assertInvalidArgument(t, err, server.CodeInvalidDomain, "domain")
		})
	}
}

func TestBaseURLValidate(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		url   server.BaseURL
		valid bool
	}{
		"https":           {"https://quire-a.example", true},
		"http":            {"http://127.0.0.1:8080", true},
		"empty":           {"", false},
		"a bare host":     {"quire-a.example", false},
		"another scheme":  {"grpc://quire-a.example", false},
		"a scheme only":   {"https://", false},
		"longer than 255": {server.BaseURL("https://" + strings.Repeat("a", 250)), false},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := testCase.url.Validate()
			if testCase.valid {
				if err != nil {
					t.Fatalf("Validate(%q) = %v, want nil", testCase.url, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate(%q) = nil, want an error", testCase.url)
			}

			assertInvalidArgument(t, err, server.CodeInvalidBaseURL, "base_url")
		})
	}
}

// TestJWKSURIIsOptional covers a peer that publishes no signing keys: its
// tokens cannot be verified here, which is a fact about that node rather than
// a malformed record.
func TestJWKSURIIsOptional(t *testing.T) {
	t.Parallel()

	var absent server.JWKSURI

	if !absent.IsZero() {
		t.Error("an unset location did not report itself absent")
	}

	if err := absent.Validate(); err != nil {
		t.Errorf("Validate on an absent location = %v, want nil", err)
	}

	if err := server.JWKSURI("keys.json").Validate(); err == nil {
		t.Error("a relative location was accepted, and nothing could fetch it")
	}
}

// TestFingerprintRequiresItsPrefix is C12 in the type system: the stored pin
// is a digest of the public key, and the prefix is what says so. A bare digest
// would be unreadable to whoever finds it later, and indistinguishable from
// the certificate digest the correction exists to replace.
func TestFingerprintRequiresItsPrefix(t *testing.T) {
	t.Parallel()

	valid := server.Fingerprint(wellknown.PinPrefix + "Zm9vYmFyCg==")
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate on a published pin = %v, want nil", err)
	}

	for name, pin := range map[string]server.Fingerprint{
		"a bare digest":          "Zm9vYmFyCg==",
		"the prefix alone":       server.Fingerprint(wellknown.PinPrefix),
		"a certificate digest":   "sha256:Zm9vYmFyCg==",
		"longer than the column": server.Fingerprint(wellknown.PinPrefix + strings.Repeat("a", 128)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := pin.Validate()
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want an error", pin)
			}

			assertInvalidArgument(t, err, server.CodeInvalidFingerprint, "certificate_fingerprint")
		})
	}
}

// TestFingerprintIsOptional covers the development profile, where the node has
// no certificate of its own and publishes no pin.
func TestFingerprintIsOptional(t *testing.T) {
	t.Parallel()

	var absent server.Fingerprint

	if !absent.IsZero() || absent.Validate() != nil {
		t.Error("a node that presents no certificate could not be described")
	}
}

// TestDescriptorValidateKeepsTheFieldError covers what a client is shown when
// one field of a description is wrong: the code and the field of that field,
// not a summary of the whole descriptor.
func TestDescriptorValidateKeepsTheFieldError(t *testing.T) {
	t.Parallel()

	err := server.Descriptor{
		Domain:  "quire-a.example",
		BaseURL: "quire-a.example",
	}.Validate()
	if err == nil {
		t.Fatal("Validate with an unusable base url = nil, want an error")
	}

	assertInvalidArgument(t, err, server.CodeInvalidBaseURL, "base_url")

	if got := errs.MessageOf(err); !strings.Contains(got, "base url") {
		t.Errorf("message = %q, and it no longer says which field failed", got)
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
