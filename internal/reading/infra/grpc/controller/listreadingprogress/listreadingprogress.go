// Package listreadingprogress serves ReadingService.ListReadingProgress (UC05,
// RN01).
package listreadingprogress

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
)

// ListReadingProgress serves the call.
type ListReadingProgress struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(list command.Usecase[usecase.Input, usecase.Output]) *ListReadingProgress {
	return &ListReadingProgress{usecase: list}
}

// Handle answers with every device's position in the work.
//
// Not paginated, as the contract has it: there is one row per appliance the
// reader has ever opened the work on, and which of them to resume from is their
// client's decision rather than this node's.
func (c *ListReadingProgress) Handle(
	ctx context.Context,
	request *quirev1.ListReadingProgressRequest,
) (*quirev1.ListReadingProgressResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	work, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{UserID: identity.UserID, EbookID: work})
	if err != nil {
		return nil, err
	}

	return &quirev1.ListReadingProgressResponse{
		Progress: convert.ProgressList(output.Progress),
	}, nil
}
