// Package listservers is the read half of UC12 for the whole catalogue
// (RF13).
package listservers

import (
	"context"

	command "github.com/anthonyvsmuller/quire/internal/federation/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
)

// ListServers reads the catalogue.
type ListServers struct {
	servers server.Repository
}

// ListServers satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListServers)(nil)

// New returns the use case over its dependencies.
func New(servers server.Repository) *ListServers {
	return &ListServers{servers: servers}
}

// Execute reads the nodes.
func (l *ListServers) Execute(ctx context.Context, input Input) (Output, error) {
	nodes, err := l.servers.List(ctx, input.IncludeInactive)
	if err != nil {
		return Output{}, err
	}

	return Output{Servers: nodes}, nil
}
