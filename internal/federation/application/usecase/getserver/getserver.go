// Package getserver is the read half of UC12 for one node (RF13).
//
// It asks nothing about who is calling. The catalogue is a property of the
// node rather than of the reader — C15 in docs/tcc-corrections.md — so there
// is no such thing here as somebody else's row, and a call that filtered by
// reader would be filtering on a column the table does not have.
package getserver

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// GetServer reads one node.
type GetServer struct {
	servers server.Repository
}

// GetServer satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*GetServer)(nil)

// New returns the use case over its dependencies.
func New(servers server.Repository) *GetServer {
	return &GetServer{servers: servers}
}

// Execute reads the node.
func (g *GetServer) Execute(ctx context.Context, input Input) (Output, error) {
	node, err := g.servers.GetByID(ctx, input.ServerID)
	if err != nil {
		return Output{}, err
	}

	return Output{Server: node}, nil
}
