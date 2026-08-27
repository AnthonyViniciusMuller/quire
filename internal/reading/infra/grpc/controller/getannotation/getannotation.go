// Package getannotation serves ReadingService.GetAnnotation (UC04, read).
package getannotation

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
)

// GetAnnotation serves the call.
type GetAnnotation struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(get command.Usecase[usecase.Input, usecase.Output]) *GetAnnotation {
	return &GetAnnotation{usecase: get}
}

// Handle answers with the mark.
func (c *GetAnnotation) Handle(
	ctx context.Context,
	request *quirev1.GetAnnotationRequest,
) (*quirev1.GetAnnotationResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Annotation(request.GetAnnotationId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, AnnotationID: id})
	if err != nil {
		return nil, err
	}

	return &quirev1.GetAnnotationResponse{
		Annotation: convert.Annotation(output.Annotation),
	}, nil
}
