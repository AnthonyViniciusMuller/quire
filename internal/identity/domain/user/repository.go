package user

import (
	"context"
	"uuid"
)

// The stable machine-readable codes a repository reports, which is where the
// two uniqueness rules of RN09 become something a client can tell apart: a
// duplicate local name and a duplicate address both arrive from the driver as
// the same unique violation, and only the constraint that was broken says
// which.
const (
	// CodeLocalNameTaken is the identifier already belonging to somebody on
	// this origin server.
	CodeLocalNameTaken = "local_name_taken"
	// CodeEmailRegistered is the address already registered on this origin
	// server.
	CodeEmailRegistered = "email_registered"
	// CodeNotFound is no such reader here.
	CodeNotFound = "user_not_found"
)

// Repository is the port through which the use cases of the identity slice read
// and write readers. It belongs to the domain; what satisfies it lives in
// internal/identity/infra/repository/user.
//
// Two conventions hold for every method.
//
// The context is passed rather than created, because the transaction manager
// carries the transaction in it: registering a reader and binding their first
// device is one unit of work, and a repository that opened its own context
// could not take part in it.
//
// A record that does not exist is an error of kind errs.KindNotFound, never a
// zero value. A zero value obliges every caller to remember a check, and the
// caller that forgets registers a second reader over the first.
type Repository interface {
	// Create inserts the reader. A local name or an address already taken on
	// this origin server is errs.KindAlreadyExists, with the code saying which
	// of the two collided (RN09).
	Create(ctx context.Context, user *User) error

	// Update writes back the mutable fields: the display name, the address and
	// the password digest, with the instant they changed at.
	Update(ctx context.Context, user *User) error

	// Delete removes the reader and everything that cascades from them. It is
	// not a migration: RF17 moves a reader to another origin server and keeps
	// the record.
	Delete(ctx context.Context, id uuid.UUID) error

	// GetByID reads a reader by primary key.
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)

	// GetByLocalName reads a reader by the pair RN09 makes unique. The origin
	// server is a parameter and not an assumption: the same local name on two
	// servers is two people, and a node holds rows for readers of both.
	GetByLocalName(ctx context.Context, originServerID uuid.UUID, localName LocalName) (*User, error)

	// GetByEmail reads a reader by address, folding case as the uniqueness
	// index does. The address is unique only within an origin server, which is
	// why that is a parameter here too.
	GetByEmail(ctx context.Context, originServerID uuid.UUID, email Email) (*User, error)
}
