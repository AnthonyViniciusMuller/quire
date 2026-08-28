// Package user is the reader as their node knows them: the entity, the value
// objects its identifier is made of, and the port a repository has to satisfy.
//
// Two rules from the specification shape everything here. A reader belongs to
// exactly one origin server, which is the only node that authenticates them
// (RN08), and their identifier is the pair of a local name and that server's
// domain rather than the local name alone (RN09) — @anthony:quire-a.example and
// @anthony:quire-b.example are two people. [FederatedID] is that pair, and it is
// assembled from the row rather than stored beside it, so the two can never
// disagree.
//
// Validation is deliberately at least as strict as the schema. A value the
// database refuses comes back as a constraint violation naming a table and a
// column, which is neither something a reader can act on nor something a node
// should say out loud; rejecting it here is what turns it into a named field
// and a reason.
package user

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by the constructor.
const opNew = "identity/user: new"

// CodeInvalidUser is the code carried by a reader that could not be built for a
// reason none of the value objects owns.
const CodeInvalidUser = "invalid_user"

// Props is everything about a reader other than their identifier.
//
// It is separated so that a repository can rebuild an entity read from the
// database — identifier and all — through the same type a constructor fills,
// and so that the fields a caller supplies are visibly not the one the node
// mints.
type Props struct {
	// OriginServerID names the node responsible for authenticating this reader
	// (RN08). It is this node's own row in federation.servers for a reader
	// registered here, and a peer's row for one this node only replicates.
	OriginServerID uuid.UUID
	// LocalName is the reader's half of the federated identifier.
	LocalName LocalName
	// DisplayName is what the reader calls themselves.
	DisplayName DisplayName
	// Email is the address on record, absent on a node that only replicates
	// this reader (RN09, C03).
	Email Email
	// PasswordHash is the digest of the reader's password, never the password,
	// and absent for the same reason Email is: a replica authenticates nobody.
	PasswordHash string
	// MigratedFrom is the identifier the reader arrived under, on a reader who
	// arrived by migrating from another origin server (RF17, UC16), and empty
	// on everybody else.
	//
	// It is provenance and never identity. C11 in docs/tcc-corrections.md is
	// the argument: a node that needs nothing from the previous server — which
	// is what makes UC16 independent of that server's availability — has
	// nothing with which to check the claim. It can record it and it cannot
	// verify it, so nothing here authenticates against it and the reader's
	// identifier is the one this node gives them.
	MigratedFrom Provenance

	// CreatedAt is when the record was opened here. It is a wall clock and,
	// unlike the timestamps on the replicable entities, it settles nothing.
	CreatedAt time.Time
	// UpdatedAt is when the record last changed here.
	UpdatedAt time.Time
}

// User is a reader, identified and described.
//
// Both kinds of reader are this type: the ones this node is the origin server
// of, who carry an address and a password digest, and the ones it merely
// replicates for a peer, who carry neither. That is C03 in
// docs/tcc-corrections.md, and [User.Authenticates] is the question asked of the
// row alone.
type User struct {
	// ID is the primary key every other table references. A migration to
	// another origin server (RF17) rewrites OriginServerID and leaves this
	// alone, which is why nothing keys off the federated identifier.
	ID uuid.UUID

	Props
}

// New builds a reader hosted by this node, which is the only kind that has an
// address and a password and therefore the only kind built here. A reader this
// node merely replicates arrives from a peer with the fields that peer was
// willing to send, and is assembled by the federation slice.
//
// The props are validated again even where the caller parsed them, because a
// value object can also be built by conversion, and an entity that exists has
// to be an entity that is valid.
func New(props *Props) (*User, error) {
	if err := props.validate(); err != nil {
		return nil, err
	}

	return &User{
		// The identifier is minted here rather than left to the column default,
		// so that the caller holds it before the insert: the first device of a
		// reader is bound in the same transaction as the reader themselves, and
		// it has to reference something.
		ID:    uuid.New(),
		Props: *props,
	}, nil
}

// Restore rebuilds a reader already stored, without minting an identifier and
// without validating: the row was validated when it was written, and a
// repository that refused to read back what the schema accepted would make a
// record unreachable rather than correct.
func Restore(id uuid.UUID, props *Props) *User {
	return &User{ID: id, Props: *props}
}

// validate reports the first rule the props break, or nil.
func (p *Props) validate() error {
	if err := p.LocalName.Validate(); err != nil {
		return err
	}

	if err := p.DisplayName.Validate(); err != nil {
		return err
	}

	if err := p.Email.Validate(); err != nil {
		return err
	}

	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the reader could not be registered").
			WithOp(opNew).
			WithCode(CodeInvalidUser).
			WithField(field, reason)
	}

	switch {
	case p.OriginServerID == (uuid.UUID{}):
		return invalid("origin_server_id", "it must name the node that authenticates the reader")
	case p.PasswordHash == "":
		return invalid("password_hash", "a reader registered here has a password")
	case p.CreatedAt.IsZero() || p.UpdatedAt.IsZero():
		return invalid("created_at", "the record has to be stamped with the instant it was opened at")
	default:
		return nil
	}
}

// Authenticates reports whether this node is the reader's origin server, which
// is the same question as whether the row carries the credentials only an
// origin server holds (RN08, C03).
func (u *User) Authenticates() bool { return u.PasswordHash != "" }

// FederatedID assembles the reader's identifier on domain, which is the domain
// of the server OriginServerID points at.
func (u *User) FederatedID(domain ServerDomain) (FederatedID, error) {
	return NewFederatedID(u.LocalName, domain)
}

// Rename records a new display name.
func (u *User) Rename(displayName DisplayName, now time.Time) error {
	if err := displayName.Validate(); err != nil {
		return err
	}

	u.DisplayName = displayName
	u.UpdatedAt = now

	return nil
}

// ChangeEmail records a new address.
//
// It does not prove the address, and nothing here can: what a wrong one costs
// is the recovery of UC08. A node that wanted proof would have to withhold the
// change until a message sent to the new address came back, which is a flow the
// specification does not describe.
func (u *User) ChangeEmail(email Email, now time.Time) error {
	if err := email.Validate(); err != nil {
		return err
	}

	u.Email = email
	u.UpdatedAt = now

	return nil
}

// ChangePassword records a new digest. The caller has already verified whatever
// it had to verify — the current password for UC06, a recovery credential for
// UC08 — and hashed the new one through the hashing port.
func (u *User) ChangePassword(passwordHash string, now time.Time) {
	u.PasswordHash = passwordHash
	u.UpdatedAt = now
}
