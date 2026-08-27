// Package listauthorizations is the read half of UC15: which nodes hold a copy
// of the reader's data, which of them hold the files, and which used to
// (RF16).
//
// It is what makes RN03 auditable. A promise that nothing leaves this node
// without the reader's permission is worth what the reader can see of it, and
// this is where they see it.
package listauthorizations

import (
	"context"
	"uuid"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// ListAuthorizations reads a reader's permissions.
type ListAuthorizations struct {
	servers  server.Repository
	replicas replica.Repository
}

// ListAuthorizations satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListAuthorizations)(nil)

// New returns the use case over its dependencies.
func New(servers server.Repository, replicas replica.Repository) *ListAuthorizations {
	return &ListAuthorizations{servers: servers, replicas: replicas}
}

// Execute reads the permissions and names the nodes they refer to.
//
// The catalogue is read whole, including the deactivated nodes: a permission
// naming a node that was later stopped still has to be shown with a name, and
// it is one of the rows a reader most needs to see.
func (l *ListAuthorizations) Execute(ctx context.Context, input Input) (Output, error) {
	authorizations, err := l.replicas.ListByUser(ctx, input.UserID, input.IncludeInactive)
	if err != nil {
		return Output{}, err
	}

	if len(authorizations) == 0 {
		return Output{Authorizations: []Authorization{}}, nil
	}

	nodes, err := l.servers.List(ctx, true)
	if err != nil {
		return Output{}, err
	}

	domains := make(map[uuid.UUID]server.Domain, len(nodes))
	for _, node := range nodes {
		domains[node.ID] = node.Domain
	}

	named := make([]Authorization, 0, len(authorizations))
	for _, authorization := range authorizations {
		// A permission always names a row that exists — the foreign key says
		// so, and it cascades — so the lookup cannot miss. It is written as a
		// lookup rather than an assertion because an absent domain renders as
		// an unnamed node, which is a better answer than a call that fails.
		named = append(named, Authorization{
			Replica:      authorization,
			ServerDomain: domains[authorization.ServerID],
		})
	}

	return Output{Authorizations: named}, nil
}
