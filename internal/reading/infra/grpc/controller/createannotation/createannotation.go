// Package createannotation serves ReadingService.CreateAnnotation (UC04,
// create).
package createannotation

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/createannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
)

// CreateAnnotation serves the call.
type CreateAnnotation struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(create command.Usecase[usecase.Input, usecase.Output]) *CreateAnnotation {
	return &CreateAnnotation{usecase: create}
}

// Handle records the mark.
//
// The reader and the device come from the token and never from the request, and
// the identifier the client sent is ignored: a mark is named by the node that
// records it, because the operation the write is appended to has to name it and
// a client that chose its own could name one that already exists. The revision
// is ignored for the reason the contract gives — a client that could stamp its
// own would be able to claim it wrote before a write it has already seen.
func (c *CreateAnnotation) Handle(
	ctx context.Context,
	request *quirev1.CreateAnnotationRequest,
) (*quirev1.CreateAnnotationResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	mark := request.GetAnnotation()

	work, err := identifier.Ebook(mark.GetEbookId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		DeviceID: identity.DeviceID,
		EbookID:  work,
		Kind:     convert.KindValue(mark.GetKind()),
		Text:     mark.GetText(),
		Locator:  mark.GetLocator(),
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.CreateAnnotationResponse{
		Annotation: convert.Annotation(output.Annotation),
	}, nil
}
