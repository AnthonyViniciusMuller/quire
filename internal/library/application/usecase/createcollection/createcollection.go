// Package createcollection is the create half of UC03 (RF05).
//
// A collection and a category are the same row with a different kind, which is
// what lets RF05 offer both without a second entity. Nothing in the node
// branches on which one a grouping is; the distinction exists so that a client
// can present a shelf and a subject differently.
package createcollection

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
)

// CreateCollection defines groupings.
type CreateCollection struct {
	collections collection.Repository
	clock       service.Clock
}

// CreateCollection satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*CreateCollection)(nil)

// New returns the use case over its dependencies.
func New(collections collection.Repository, clock service.Clock) *CreateCollection {
	return &CreateCollection{collections: collections, clock: clock}
}

// Execute writes the row.
//
// The name is not made unique. A reader may well have two shelves called
// "later", and a node that refused the second would be enforcing a rule
// neither the schema nor the specification has — one that two offline devices
// could not obey anyway, since neither can see the other's shelves until they
// meet.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (c *CreateCollection) Execute(ctx context.Context, input Input) (Output, error) {
	details, err := parseDetails(&input)
	if err != nil {
		return Output{}, err
	}

	grouping, err := collection.New(input.UserID, details, input.DeviceID, c.clock.Now())
	if err != nil {
		return Output{}, err
	}

	if err := c.collections.Create(ctx, grouping); err != nil {
		return Output{}, err
	}

	return Output{Collection: grouping}, nil
}

// parseDetails turns what the client sent into the description the entity
// takes.
func parseDetails(input *Input) (*collection.Details, error) {
	name, err := collection.ParseName(input.Name)
	if err != nil {
		return nil, err
	}

	kind, err := collection.ParseKind(input.Kind)
	if err != nil {
		return nil, err
	}

	description, err := collection.ParseDescription(input.Description)
	if err != nil {
		return nil, err
	}

	return &collection.Details{Name: name, Kind: kind, Description: description}, nil
}
