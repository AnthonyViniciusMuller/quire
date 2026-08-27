// Package listannotations serves ReadingService.ListAnnotations (UC04, read).
package listannotations

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listannotations"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/pagetoken"
)

// ListAnnotations serves the call.
type ListAnnotations struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListAnnotations {
	return &ListAnnotations{usecase: list}
}

// Handle answers with one page of what the reader wrote in the work.
func (c *ListAnnotations) Handle(
	ctx context.Context,
	request *quirev1.ListAnnotationsRequest,
) (*quirev1.ListAnnotationsResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	work, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	cursor, err := pagetoken.Decode(request.GetPageToken())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		EbookID:  work,
		PageSize: int(request.GetPageSize()),
		Cursor:   cursor,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.ListAnnotationsResponse{
		Annotations:   convert.Annotations(output.Annotations),
		NextPageToken: pagetoken.Encode(output.NextCursor),
	}, nil
}
