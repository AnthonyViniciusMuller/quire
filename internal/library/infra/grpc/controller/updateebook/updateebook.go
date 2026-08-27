// Package updateebook serves LibraryService.UpdateEbook (UC01, update; RF05).
package updateebook

import (
	"context"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	usecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/updateebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/fieldmask"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/identifier"
)

// The paths the mask may name. They are the fields of Ebook a reader may
// correct, and nothing else: the format, the digest and the length describe the
// file, which is fixed at import.
const (
	pathTitle     = "title"
	pathAuthor    = "author"
	pathPublisher = "publisher"
	pathLanguage  = "language"
	pathExtra     = "extra_metadata"
)

// UpdateEbook serves the call.
type UpdateEbook struct {
	usecase command.Usecase[usecase.Input, usecase.Output]
}

// New returns the controller over the use case.
func New(update command.Usecase[usecase.Input, usecase.Output]) *UpdateEbook {
	return &UpdateEbook{usecase: update}
}

// Handle applies the fields the mask named.
//
// A mask naming a path this call cannot write is refused rather than ignored.
// The two are very different to a client: an ignored path is a change it
// believes it made, and on a per-field last-writer-wins entity a change nobody
// made is a change that stays unmade for as long as nobody looks.
func (c *UpdateEbook) Handle(
	ctx context.Context,
	request *quirev1.UpdateEbookRequest,
) (*quirev1.UpdateEbookResponse, error) {
	identity, err := authn.Require(ctx)
	if err != nil {
		return nil, err
	}

	id, err := identifier.Ebook(request.GetEbookId())
	if err != nil {
		return nil, err
	}

	claimed, err := fieldmask.Claimed(request.GetUpdateMask(),
		pathTitle, pathAuthor, pathPublisher, pathLanguage, pathExtra)
	if err != nil {
		return nil, err
	}

	work := request.GetEbook()
	changes := usecase.Changes{}

	if claimed[pathTitle] {
		title := work.GetTitle()
		changes.Title = &title
	}

	if claimed[pathAuthor] {
		author := work.GetAuthor()
		changes.Author = &author
	}

	if claimed[pathPublisher] {
		publisher := work.GetPublisher()
		changes.Publisher = &publisher
	}

	if claimed[pathLanguage] {
		language := work.GetLanguage()
		changes.Language = &language
	}

	if claimed[pathExtra] {
		extra := work.GetExtraMetadata().AsMap()
		changes.Extra = &extra
	}

	output, err := c.usecase.Execute(ctx, usecase.Input{
		UserID:   identity.UserID,
		DeviceID: identity.DeviceID,
		EbookID:  id,
		Changes:  changes,
	})
	if err != nil {
		return nil, err
	}

	return &quirev1.UpdateEbookResponse{Ebook: convert.Ebook(output.Ebook)}, nil
}
