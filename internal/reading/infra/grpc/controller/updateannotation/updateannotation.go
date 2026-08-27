// Package updateannotation serves ReadingService.UpdateAnnotation (UC04,
// update; RF03).
package updateannotation

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx/fieldmask"
)

// The paths the mask may name. They are the fields of Annotation a reader may
// write, and nothing else: the identifier names the row, the work is what the
// mark is in and does not move, and the revision is the server's.
const (
	pathKind    = "kind"
	pathText    = "text"
	pathLocator = "locator"
)

// UpdateAnnotation serves the call.
type UpdateAnnotation struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateAnnotation {
	return &UpdateAnnotation{usecase: update}
}

// Handle applies the fields the mask named.
//
// A mask naming a path this call cannot write is refused rather than ignored.
// The two are very different to a client: an ignored path is a change it
// believes it made, and on a per-field last-writer-wins entity a change nobody
// made is a change that stays unmade for as long as nobody looks.
func (c *UpdateAnnotation) Handle(
	ctx context.Context,
	request *quirev1.UpdateAnnotationRequest,
) (*quirev1.UpdateAnnotationResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Annotation(request.GetAnnotationId())
	if err != nil {
		return nil, err
	}

	claimed, err := fieldmask.Claimed(request.GetUpdateMask(), pathKind, pathText, pathLocator)
	if err != nil {
		return nil, err
	}

	mark := request.GetAnnotation()
	changes := usecase.Changes{}

	if claimed[pathKind] {
		kind := convert.KindValue(mark.GetKind())
		changes.Kind = &kind
	}

	if claimed[pathText] {
		text := mark.GetText()
		changes.Text = &text
	}

	if claimed[pathLocator] {
		place := mark.GetLocator()
		changes.Locator = &place
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:       identity.UserID,
		DeviceID:     identity.DeviceID,
		AnnotationID: id,
		Changes:      changes,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateAnnotationResponse{
		Annotation: convert.Annotation(output.Annotation),
	}, nil
}
