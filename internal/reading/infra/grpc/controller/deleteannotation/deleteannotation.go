// Package deleteannotation serves ReadingService.DeleteAnnotation (UC04,
// delete).
package deleteannotation

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/deleteannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
)

// DeleteAnnotation serves the call.
type DeleteAnnotation struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(remove command.Usecase[usecase.Input, usecase.Output]) *DeleteAnnotation {
	return &DeleteAnnotation{usecase: remove}
}

// Handle tombstones the mark.
//
// The reply carries nothing, as the contract has it. What was written is the
// tombstone, and a client that wants to see it asks for the operation rather
// than for the row.
func (c *DeleteAnnotation) Handle(
	ctx context.Context,
	request *quirev1.DeleteAnnotationRequest,
) (*quirev1.DeleteAnnotationResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Annotation(request.GetAnnotationId())
	if err != nil {
		return nil, err
	}

	if _, err = c.usecase.Execute(ctx, usecase.Input{
		UserID:       identity.UserID,
		DeviceID:     identity.DeviceID,
		AnnotationID: id,
	}); err != nil {
		return nil, err
	}

	return &quirev1.DeleteAnnotationResponse{}, nil
}
