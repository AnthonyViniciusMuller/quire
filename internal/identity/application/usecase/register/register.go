// Package register is UC14: it creates a reader on this node and, in doing so,
// binds them to it.
//
// The binding is the point. The identifier that comes out is
// @local_name:<this node's domain>, so the server half is not something the
// caller chose — it is which node the call was addressed to (RN08), and the
// reader's origin_server_id points at this node's own row in the catalogue from
// then on. UC13, the discovery that tells a client which node to address, is
// what UC14 includes and is served by the federation slice.
//
// It is also the one call in this slice that both writes a password and answers
// an unauthenticated caller, which is why validation happens before anything
// expensive and why nothing here reports whether a name was already taken until
// the index says so.
package register

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// Register satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*Register)(nil)

// Register creates readers.
type Register struct {
	users       user.Repository
	hasher      service.HashService
	localServer service.LocalServer
	clock       service.Clock
}

// New returns the use case over its dependencies.
func New(
	users user.Repository,
	hasher service.HashService,
	localServer service.LocalServer,
	clock service.Clock,
) *Register {
	return &Register{users: users, hasher: hasher, localServer: localServer, clock: clock}
}

// Execute registers the reader.
//
// There is no lookup before the insert to see whether the name or the address is
// free, and that is deliberate. Such a lookup answers a question that stops
// being true the moment it is answered: two registrations racing for the same
// name both find it available, and both go on to insert. What decides is the
// unique index of RN09, which cannot race with itself, and the repository turns
// the violation it raises into an error naming which of the two collided. A
// lookup would only make the common case prettier and the rare case wrong.
func (r *Register) Execute(ctx context.Context, input Input) (Output, error) {
	fields, err := parse(input)
	if err != nil {
		return Output{}, err
	}

	originServerID, err := r.localServer.ID(ctx)
	if err != nil {
		return Output{}, err
	}

	passwordHash, err := r.hasher.Hash(string(fields.password))
	if err != nil {
		return Output{}, err
	}

	now := r.clock.Now()

	record, err := user.New(&user.Props{
		OriginServerID: originServerID,
		LocalName:      fields.localName,
		DisplayName:    fields.displayName,
		Email:          fields.email,
		PasswordHash:   passwordHash,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		return Output{}, err
	}

	err = r.users.Create(ctx, record)
	if err != nil {
		return Output{}, err
	}

	federatedID, err := record.FederatedID(r.localServer.Domain())
	if err != nil {
		return Output{}, err
	}

	return Output{User: record, FederatedID: federatedID}, nil
}

// parsedInput is what the request looks like once every field has become the
// value object that carries its rule.
type parsedInput struct {
	localName   user.LocalName
	displayName user.DisplayName
	email       user.Email
	password    user.Password
}

// parse turns the request into value objects, rejecting the first field that
// breaks its rule.
//
// All of it happens before the password is hashed, so that a malformed request
// does not cost the node a bcrypt — which is the most expensive thing this call
// does, and the only part of it an unauthenticated caller can ask for
// repeatedly.
func parse(input Input) (parsedInput, error) {
	localName, err := user.ParseLocalName(input.LocalName)
	if err != nil {
		return parsedInput{}, err
	}

	displayName, err := user.ParseDisplayName(input.DisplayName)
	if err != nil {
		return parsedInput{}, err
	}

	email, err := user.ParseEmail(input.Email)
	if err != nil {
		return parsedInput{}, err
	}

	password := user.Password(input.Password)

	err = password.Validate()
	if err != nil {
		return parsedInput{}, err
	}

	return parsedInput{localName: localName, displayName: displayName, email: email, password: password}, nil
}
