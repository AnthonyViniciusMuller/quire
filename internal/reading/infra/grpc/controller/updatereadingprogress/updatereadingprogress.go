// Package updatereadingprogress serves ReadingService.UpdateReadingProgress
// (UC05, RF02).
package updatereadingprogress

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
)

// UpdateReadingProgress serves the call.
type UpdateReadingProgress struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateReadingProgress {
	return &UpdateReadingProgress{usecase: update}
}

// Handle records where the calling device has reached.
//
// The device comes from the token, and the request has no field that could
// carry another: the row belongs to one device and that device is its only
// writer, so a request that could name one would be a request that could move
// another device's bookmark (RN10).
//
// The proportion is passed through as a pointer, because absence and zero are
// different claims: a reader at the very start of a work is at zero, and a
// client that cannot compute a proportion sends nothing.
func (c *UpdateReadingProgress) Handle(
	ctx context.Context,
	request *quirev1.UpdateReadingProgressRequest,
) (*quirev1.UpdateReadingProgressResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	work, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		DeviceID: identity.DeviceID,
		EbookID:  work,
		Locator:  request.GetLocator(),
		Percent:  request.Percent,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateReadingProgressResponse{
		Progress: convert.Progress(output.Progress),
	}, nil
}
