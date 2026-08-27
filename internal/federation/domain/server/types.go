package server

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseDomain      = "federation/server: parse domain"
	opParseBaseURL     = "federation/server: parse base url"
	opParseJWKSURI     = "federation/server: parse jwks uri"
	opParseFingerprint = "federation/server: parse fingerprint"
	opParseGRPC        = "federation/server: parse grpc authority"
)

// The stable machine-readable codes this package attaches to the errors it
// raises. A client branches on these and never on the message.
const (
	// CodeInvalidDomain is the authority not being a host.
	CodeInvalidDomain = "invalid_server_domain"
	// CodeInvalidBaseURL is an origin this node cannot address.
	CodeInvalidBaseURL = "invalid_base_url"
	// CodeInvalidJWKSURI is a signing key location this node cannot address.
	CodeInvalidJWKSURI = "invalid_jwks_uri"
	// CodeInvalidFingerprint is a pin that is not in the form C12 settled on.
	CodeInvalidFingerprint = "invalid_certificate_fingerprint"
	// CodeInvalidGRPCAuthority is an address no gRPC client could dial.
	CodeInvalidGRPCAuthority = "invalid_grpc_authority"
)

// The widths federation.servers declares, counted in characters as PostgreSQL
// counts a varchar.
const (
	maxDomainLength      = 255
	maxURLLength         = 255
	maxAuthorityLength   = 255
	maxFingerprintLength = 128
)

// Domain is the authority a node is known by: the value after the colon in
// @anthony:quire-a.example, and the host a .well-known lookup is addressed to.
//
// The identity slice holds the same value as user.ServerDomain, and the two
// are deliberately separate types. A slice's domain depends on nothing outside
// itself and the shared core, so a type shared between two of them would be a
// coupling in the layer that must have none; what the two do share is
// federation.servers_domain_format, which is where the rule actually lives.
// The one place they meet is the adapter that satisfies the identity slice's
// LocalServer port, and converting there is a line of code.
type Domain string

// String renders the domain.
func (d Domain) String() string { return string(d) }

// ParseDomain folds s to lower case and removes the surrounding space, which
// is the form the column holds and the form a lookup is addressed to. A reader
// who types Quire-A.Example means the same node as one who types it in lower
// case, and two rows for one node would be two pins for one key.
func ParseDomain(s string) Domain {
	return Domain(strings.ToLower(strings.TrimSpace(s)))
}

// Validate reports why the domain is not a host, or nil.
//
// The rule is the one federation.servers_domain_format checks: a host, in
// lower case, optionally with a port. Anything a URL would have to escape
// cannot appear in it, because the whole federation addresses the node by it.
func (d Domain) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the server domain is not a valid host").
			WithOp(opParseDomain).
			WithCode(CodeInvalidDomain).
			WithField("domain", reason)
	}

	host, port, hasPort := strings.Cut(string(d), ":")

	switch {
	case host == "":
		return invalid("it must not be empty")
	case characterCount(string(d)) > maxDomainLength:
		return invalid("it must be at most 255 characters long")
	case hasPort && !isPort(port):
		return invalid("its port must be one to five digits")
	}

	for index, character := range host {
		switch {
		case isAlphanumeric(character):
		case character == '.' || character == '-':
			if index == 0 || index == len(host)-1 {
				return invalid("it must start and end with a letter or a digit")
			}
		default:
			return invalid("it may contain only lower-case letters, digits, dots and hyphens")
		}
	}

	return nil
}

// BaseURL is where a node actually answers over HTTP, as its own discovery
// document reports it.
//
// It is separate from the domain because the specification allows a node to be
// identified by one host and served from another, and separate from the gRPC
// authority because those two are only the same address where a gateway
// happens to collapse them — D06 in docs/tcc-corrections.md.
type BaseURL string

// String renders the base URL.
func (b BaseURL) String() string { return string(b) }

// Validate reports why the base URL cannot be dialed, or nil. The scheme check
// is the one federation.servers_base_url_scheme makes.
func (b BaseURL) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the server base url is not usable").
			WithOp(opParseBaseURL).
			WithCode(CodeInvalidBaseURL).
			WithField("base_url", reason)
	}

	if string(b) == "" {
		return invalid("it must not be empty")
	}

	return validateHTTPURL(string(b), invalid)
}

// JWKSURI is where a node publishes the public keys its tokens are signed with
// (RNF11). It is absent on a node whose document names none.
type JWKSURI string

// String renders the location.
func (j JWKSURI) String() string { return string(j) }

// IsZero reports whether the node published none.
func (j JWKSURI) IsZero() bool { return string(j) == "" }

// Validate reports why the location cannot be fetched, or nil. An absent one
// is valid: a peer that publishes no keys is a peer whose tokens this node
// cannot verify, which is a fact about it rather than a malformed record.
func (j JWKSURI) Validate() error {
	if j.IsZero() {
		return nil
	}

	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the server jwks location is not usable").
			WithOp(opParseJWKSURI).
			WithCode(CodeInvalidJWKSURI).
			WithField("jwks_uri", reason)
	}

	return validateHTTPURL(string(j), invalid)
}

// Fingerprint is the pin node-to-node mTLS is checked against (RNF08), in the
// form spki-sha256:<base64>.
//
// It is a digest of the public key and not of the certificate, and C12 in
// docs/tcc-corrections.md is why: a digest of the certificate stops matching
// on the first ACME renewal, so pinning it would break replication between
// every pair of nodes about every sixty days and would train an operator to
// clear the one alarm a substituted certificate would raise.
//
// It is absent on a node that presents no certificate of its own, which is the
// development profile.
type Fingerprint string

// String renders the pin.
func (f Fingerprint) String() string { return string(f) }

// IsZero reports whether the node published no pin.
func (f Fingerprint) IsZero() bool { return string(f) == "" }

// Validate reports why the pin is not one this node could ever check against,
// or nil.
//
// The prefix is required because it is what makes the value self-describing: a
// pin that says what it is over can be replaced later by one over something
// else without a reader having to guess which they are holding. Nothing checks
// the base64 that follows it, since the only thing that produces one is the
// discovery client reading a peer's document — a reader never types a pin, and
// the contract gives them no field to type it into.
func (f Fingerprint) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the certificate pin is not usable").
			WithOp(opParseFingerprint).
			WithCode(CodeInvalidFingerprint).
			WithField("certificate_fingerprint", reason)
	}

	switch {
	case f.IsZero():
		return nil
	case !strings.HasPrefix(string(f), wellknown.PinPrefix):
		return invalid("it must be a public key digest, in the form " + wellknown.PinPrefix + "<base64>")
	case len(string(f)) == len(wellknown.PinPrefix):
		return invalid("it carries no digest after " + wellknown.PinPrefix)
	case characterCount(string(f)) > maxFingerprintLength:
		return invalid("it must be at most 128 characters long")
	default:
		return nil
	}
}

// GRPCAuthority is the host:port a peer dials for the API, as the node's own
// discovery document publishes it.
//
// It is separate from the base URL because the two are only the same address
// where a gateway happens to collapse them — D06 in docs/tcc-corrections.md.
// The .well-known documents are plain HTTP because RFC 8615 requires it and
// the API is gRPC, so without a mesh in front they listen on different ports,
// and a peer that learned only the base URL would have nowhere to dial.
//
// It is absent on a node whose document publishes none. That node can be
// recorded and cannot be replicated to, which is a fact about it rather than a
// malformed record — and refusing it here would turn a peer that is merely
// unreachable into a peer that cannot be described.
type GRPCAuthority string

// String renders the authority.
func (g GRPCAuthority) String() string { return string(g) }

// IsZero reports whether the node published none.
func (g GRPCAuthority) IsZero() bool { return string(g) == "" }

// Validate reports why the authority could not be dialed, or nil.
//
// The port is required, unlike in a Domain. That is the whole reason the value
// exists: the address a peer dials is precisely the one the base URL does not
// imply, and an authority without a port would silently mean 443 — the port
// the HTTP listener answers on in the deployment where the two do differ.
func (g GRPCAuthority) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the server grpc authority is not usable").
			WithOp(opParseGRPC).
			WithCode(CodeInvalidGRPCAuthority).
			WithField("grpc", reason)
	}

	if g.IsZero() {
		return nil
	}

	host, port, hasPort := strings.Cut(string(g), ":")

	switch {
	case !hasPort:
		return invalid("it must carry a port, since it is not the one the base url implies")
	case !isPort(port):
		return invalid("its port must be one to five digits")
	case characterCount(string(g)) > maxAuthorityLength:
		return invalid("it must be at most 255 characters long")
	}

	if err := Domain(host).Validate(); err != nil {
		return invalid("its host may contain only lower-case letters, digits, dots and hyphens")
	}

	return nil
}

// Descriptor is everything a node publishes about itself, as discovery returns
// it. It is the mutable half of a catalogue row: the fields below are learned
// from the node and refreshed, never typed.
type Descriptor struct {
	// Domain is the authority the lookup was addressed to.
	Domain Domain
	// BaseURL is where the node answers over HTTP.
	BaseURL BaseURL
	// JWKSURI is where it publishes its signing keys (RNF11).
	JWKSURI JWKSURI
	// CertificateFingerprint is the pin for node-to-node mTLS (RNF08).
	CertificateFingerprint Fingerprint
	// GRPCAuthority is where the API answers (D06).
	GRPCAuthority GRPCAuthority
}

// Validate reports the first field of the descriptor that cannot be stored, or
// nil.
//
// The receiver is a pointer for the reason every other heavy value in this
// repository is passed as one: five strings is eighty bytes, and copying them
// at each of the four layers a description crosses is a copy nobody asked for.
//
// It returns what the field itself raised rather than wrapping it. The value
// objects already say which field failed, why, and under which code, and an
// outer message would replace all three with a vaguer one — the message a
// client is shown is the outermost, and this is not the layer that knows more
// than the field does.
func (d *Descriptor) Validate() error {
	for _, validate := range []func() error{
		d.Domain.Validate,
		d.BaseURL.Validate,
		d.JWKSURI.Validate,
		d.CertificateFingerprint.Validate,
		d.GRPCAuthority.Validate,
	} {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

// validateHTTPURL is the check both URL-valued types make: an absolute http or
// https URL, narrow enough for the column that holds it.
func validateHTTPURL(value string, invalid func(reason string) error) error {
	if characterCount(value) > maxURLLength {
		return invalid("it must be at most 255 characters long")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return invalid("it is not a url")
	}

	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return invalid("its scheme must be http or https")
	case parsed.Host == "":
		return invalid("it must name a host")
	default:
		return nil
	}
}

// isAlphanumeric reports whether r is a lower-case letter or a digit, which is
// what a host may begin and end with.
func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// isPort reports whether s is the one to five digits a port is written as.
func isPort(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}

	for _, character := range s {
		if character < '0' || character > '9' {
			return false
		}
	}

	return true
}

// characterCount is the length PostgreSQL measures a varchar in: characters,
// not bytes.
func characterCount(s string) int { return utf8.RuneCountInString(s) }
