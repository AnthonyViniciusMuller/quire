package user

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opParseLocalName    = "identity/user: parse local name"
	opParseDisplayName  = "identity/user: parse display name"
	opParseEmail        = "identity/user: parse email"
	opParseFederatedID  = "identity/user: parse federated id"
	opParseServerDomain = "identity/user: parse server domain"
)

// The stable machine-readable codes this package attaches to the errors it
// raises. A client branches on these and never on the message: the code is part
// of the contract and survives a wording fix, while the message is meant to be
// read by a person.
const (
	// CodeInvalidLocalName is the local name half of a federated identifier
	// failing the shape RN09 makes unique.
	CodeInvalidLocalName = "invalid_local_name"
	// CodeInvalidDisplayName is the shown name being blank or too long.
	CodeInvalidDisplayName = "invalid_display_name"
	// CodeInvalidEmail is an address this node cannot store or deliver to.
	CodeInvalidEmail = "invalid_email"
	// CodeInvalidFederatedID is an identifier that is not @local_name:domain.
	CodeInvalidFederatedID = "invalid_federated_id"
	// CodeInvalidServerDomain is the domain half of a federated identifier not
	// being a host.
	CodeInvalidServerDomain = "invalid_server_domain"
)

// The widths identity.users declares, counted in characters as PostgreSQL
// counts a varchar.
const (
	maxLocalNameLength   = 64
	maxDisplayNameLength = 120
	maxEmailLength       = 255
)

// LocalName is the first half of a federated identifier: the anthony in
// @anthony:quire-a.example.
//
// The shape is the one identity.users.local_name enforces, and it is narrow on
// purpose. The value is embedded in a URL, in the subject of a JWT and in a
// .well-known lookup, and each of those escapes a different set of characters;
// a name that needs no escaping anywhere cannot be mangled by any of them.
type LocalName string

// String renders the local name.
func (l LocalName) String() string { return string(l) }

// Validate reports why the local name is not one, or nil.
//
// The rule is the one identity.users_local_name_format checks: one to
// sixty-four characters, lower-case letters, digits and the three separators,
// beginning and ending with a letter or a digit. It is written out rather than
// compiled as a regular expression because the loop can say which of those four
// parts was broken, and a reader can act on that — and because a compiled
// pattern would be the package-level variable the project's linter forbids.
func (l LocalName) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the local name is not a valid identifier").
			WithOp(opParseLocalName).
			WithCode(CodeInvalidLocalName).
			WithField("local_name", reason)
	}

	name := string(l)

	switch {
	case name == "":
		return invalid("it must not be empty")
	case len(name) > maxLocalNameLength:
		return invalid("it must be at most 64 characters long")
	}

	for index, character := range name {
		switch {
		case isAlphanumeric(character):
		case isLocalNameSeparator(character):
			// A separator is allowed only between two other characters. The
			// index is a byte offset, which is the same as a character offset
			// here: everything this loop accepts is one byte wide, and anything
			// wider has already failed the case above.
			if index == 0 || index == len(name)-1 {
				return invalid("it must start and end with a letter or a digit")
			}
		default:
			return invalid("it may contain only lower-case letters, digits, and the separators . - _")
		}
	}

	return nil
}

// ParseLocalName folds s to lower case, removes the surrounding space, and
// validates the result.
//
// Folding is what makes the identifier one identifier. @Anthony and @anthony
// differ only in how they were typed, while the uniqueness of RN09 is over the
// stored value, so accepting both would hand the second reader the account the
// first believes is theirs. The schema refuses an upper-case name outright, so
// folding here is also what registers a reader who capitalizes their own name
// instead of rejecting them.
func ParseLocalName(s string) (LocalName, error) {
	name := LocalName(strings.ToLower(strings.TrimSpace(s)))
	if err := name.Validate(); err != nil {
		return "", err
	}

	return name, nil
}

// DisplayName is what a reader calls themselves, and the only name meant to be
// shown.
type DisplayName string

// String renders the display name.
func (d DisplayName) String() string { return string(d) }

// Validate reports why the display name is not usable, or nil. The two rules
// are that it is not blank — which the schema does not check, and which would
// leave a reader rendered as nothing — and that it fits the column.
func (d DisplayName) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the display name is not usable").
			WithOp(opParseDisplayName).
			WithCode(CodeInvalidDisplayName).
			WithField("display_name", reason)
	}

	switch {
	case string(d) == "":
		return invalid("it must not be empty")
	case characterCount(string(d)) > maxDisplayNameLength:
		return invalid("it must be at most 120 characters long")
	default:
		return nil
	}
}

// ParseDisplayName removes the surrounding space from s and validates the
// result.
func ParseDisplayName(s string) (DisplayName, error) {
	name := DisplayName(strings.TrimSpace(s))
	if err := name.Validate(); err != nil {
		return "", err
	}

	return name, nil
}

// Email is a reader's address, as identity.users.email holds it.
//
// It is personal data and is deliberately kept out of the replicated set
// (RN09), so it exists only on the node that authenticates the reader, and is
// returned to nobody but that reader (C03 in docs/tcc-corrections.md).
type Email string

// String renders the address as the reader typed it.
func (e Email) String() string { return string(e) }

// IsZero reports whether the address is absent, which is what a reader this
// node only replicates looks like.
func (e Email) IsZero() bool { return e == "" }

// Fold returns the address in the form uniqueness is decided on: lower case.
//
// The index enforcing RN09 is over lower(email), so this is what a lookup
// compares and what a second registration collides with. The stored value keeps
// the reader's own capitalization, because the address is shown back to them
// and nothing is gained by rewriting it.
func (e Email) Fold() string { return strings.ToLower(string(e)) }

// Validate reports why the address is not usable, or nil.
//
// The rule is the one identity.users_email_format checks — something, an at
// sign, something, none of it blank — and it is deliberately not an attempt at
// RFC 5322. An address is proved to exist by sending to it, which UC08 already
// does; a stricter pattern would only reject addresses that work.
func (e Email) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the e-mail address is not usable").
			WithOp(opParseEmail).
			WithCode(CodeInvalidEmail).
			WithField("email", reason)
	}

	address := string(e)
	if characterCount(address) > maxEmailLength {
		return invalid("it must be at most 255 characters long")
	}

	local, host, found := strings.Cut(address, "@")

	switch {
	case !found:
		return invalid("it must contain an at sign")
	case local == "" || host == "":
		return invalid("it must have a name before the at sign and a host after it")
	case strings.ContainsRune(host, '@'):
		return invalid("it must contain exactly one at sign")
	case strings.ContainsFunc(address, unicode.IsSpace):
		return invalid("it must not contain spaces")
	default:
		return nil
	}
}

// ParseEmail removes the surrounding space from s and validates the result.
func ParseEmail(s string) (Email, error) {
	address := Email(strings.TrimSpace(s))
	if err := address.Validate(); err != nil {
		return "", err
	}

	return address, nil
}

// ServerDomain is the domain half of a federated identifier: the authority a
// .well-known lookup is addressed to, and the value federation.servers.domain
// holds.
type ServerDomain string

// String renders the domain.
func (s ServerDomain) String() string { return string(s) }

// ParseServerDomain folds s to lower case and removes the surrounding space. It
// does not validate on its own, because every constructor in this package
// validates what it is handed.
func ParseServerDomain(s string) ServerDomain {
	return ServerDomain(strings.ToLower(strings.TrimSpace(s)))
}

// Validate reports why the domain is not a host, or nil.
//
// The rule is the one federation.servers_domain_format checks: a host, in lower
// case, optionally with a port. Anything a URL would have to escape cannot
// appear in it, because the whole federation addresses this node by it.
func (s ServerDomain) Validate() error {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the server domain is not a valid host").
			WithOp(opParseServerDomain).
			WithCode(CodeInvalidServerDomain).
			WithField("server_domain", reason)
	}

	host, port, hasPort := strings.Cut(string(s), ":")

	switch {
	case host == "":
		return invalid("it must not be empty")
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

// FederatedID is a reader's whole identifier, @local_name:domain.
//
// It is assembled and never stored. The row holds the local name and a
// reference to the origin server, so an identifier built from it cannot
// disagree with the record it describes — and a migration to another origin
// server (RF17) rewrites one column rather than every copy of a string.
type FederatedID struct {
	// LocalName is the half the reader chose.
	LocalName LocalName
	// Domain is the half the origin server contributes (RN08).
	Domain ServerDomain
}

// NewFederatedID assembles the identifier of localName on domain.
func NewFederatedID(localName LocalName, domain ServerDomain) (FederatedID, error) {
	if err := domain.Validate(); err != nil {
		return FederatedID{}, err
	}

	if err := localName.Validate(); err != nil {
		return FederatedID{}, err
	}

	return FederatedID{LocalName: localName, Domain: domain}, nil
}

// String renders the identifier the way every client is meant to show it.
func (f FederatedID) String() string {
	return "@" + string(f.LocalName) + ":" + string(f.Domain)
}

// ParseFederatedID reads an identifier back into its two halves.
//
// A reader types this, so it is parsed from the outside in: the leading at
// sign, then the local name up to the first colon, then the domain. A local
// name cannot contain a colon, which is what makes the split unambiguous even
// where the domain carries a port.
func ParseFederatedID(s string) (FederatedID, error) {
	invalid := func(reason string) error {
		return errs.New(errs.KindInvalidArgument, "the identifier is not in the form @name:server").
			WithOp(opParseFederatedID).
			WithCode(CodeInvalidFederatedID).
			WithField("federated_id", reason)
	}

	rest, found := strings.CutPrefix(strings.TrimSpace(s), "@")
	if !found {
		return FederatedID{}, invalid("it must start with an at sign")
	}

	name, domain, found := strings.Cut(rest, ":")
	if !found {
		return FederatedID{}, invalid("it must name a server after a colon")
	}

	localName, err := ParseLocalName(name)
	if err != nil {
		return FederatedID{}, err
	}

	return NewFederatedID(localName, ParseServerDomain(domain))
}

// isAlphanumeric reports whether r is a lower-case letter or a digit, which is
// what both a local name and a host may begin and end with.
func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// isLocalNameSeparator reports whether r may appear inside a local name.
func isLocalNameSeparator(r rune) bool {
	return r == '.' || r == '-' || r == '_'
}

// isPort reports whether s is one to five digits, which is the whole of what
// the schema accepts after the colon.
func isPort(s string) bool {
	const maxPortDigits = 5

	if s == "" || len(s) > maxPortDigits {
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
// not bytes. A name of sixty accented characters fits varchar(120) and would be
// rejected by a byte count.
func characterCount(s string) int { return utf8.RuneCountInString(s) }
