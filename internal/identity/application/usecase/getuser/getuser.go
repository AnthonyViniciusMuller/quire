// Package getuser answers a reader with their own record (UC06, read).
//
// It takes no identifier but the one in the session, and that is the whole
// access rule: a node never serves one reader's record to another. The address
// is personal data kept out of the replicated set on purpose (RN09), and the
// only party entitled to read it is the reader themselves.
package getuser

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/identity/application/service"
	command "github.com/anthonyvsmuller/quire/internal/identity/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/identity/domain/user"
)

// GetUser reads readers.
type GetUser struct {
	users       user.Repository
	localServer service.LocalServer
}

// GetUser satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*GetUser)(nil)

// New returns the use case over its dependencies.
func New(users user.Repository, localServer service.LocalServer) *GetUser {
	return &GetUser{users: users, localServer: localServer}
}

// Execute reads the reader's own record.
func (g *GetUser) Execute(ctx context.Context, input Input) (Output, error) {
	reader, err := g.users.GetByID(ctx, input.UserID)
	if err != nil {
		return Output{}, err
	}

	federatedID, err := reader.FederatedID(g.localServer.Domain())
	if err != nil {
		return Output{}, err
	}

	return Output{User: reader, FederatedID: federatedID}, nil
}
