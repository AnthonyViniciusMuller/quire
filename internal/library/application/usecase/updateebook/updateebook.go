// Package updateebook edits the description of a work (RF05).
//
// UC01 is marked «CRD» because the file itself is not editable. Its
// description is, and this is the whole of that: the title, the author, the
// publisher, the language and whatever else the format carried.
package updateebook

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/updateebook: execute"

// CodeEmptyUpdate is an edit that claims no field at all.
const CodeEmptyUpdate = "empty_update"

// UpdateEbook edits works.
type UpdateEbook struct {
	works ebook.Repository
	clock service.Clock
}

// UpdateEbook satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateEbook)(nil)

// New returns the use case over its dependencies.
func New(works ebook.Repository, clock service.Clock) *UpdateEbook {
	return &UpdateEbook{works: works, clock: clock}
}

// Execute applies the fields the mask named and stamps the write.
//
// An edit that claims nothing is refused rather than served as a no-op. It
// would not be a no-op: it would stamp a revision, and a version that claims a
// write nobody made would win a tie-break against a real edit from a device
// that had been offline.
//
// The read and the write are not wrapped in a transaction, and the reason is
// that the version they would protect is not the one that matters. Two devices
// editing the same work at the same moment is exactly what the vector clock
// exists to resolve, and it resolves it whether or not the two writes were
// serialized here — the loser's write is a version, not a lost update. What a
// transaction would add is that the second write is derived from the first,
// which on this entity is not worth a lock.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (u *UpdateEbook) Execute(ctx context.Context, input Input) (Output, error) {
	if input.Changes.IsEmpty() {
		return Output{}, errs.New(errs.KindInvalidArgument, "the edit claims no field").
			WithOp(opExecute).
			WithCode(CodeEmptyUpdate).
			WithField("update_mask", "it must name at least one field to write")
	}

	work, err := u.works.GetByID(ctx, input.EbookID)
	if err != nil {
		return Output{}, err
	}

	if !work.BelongsTo(input.UserID) || work.IsDeleted() {
		return Output{}, getebook.NotFound(opExecute)
	}

	details, err := apply(&work.Details, &input.Changes)
	if err != nil {
		return Output{}, err
	}

	if err := work.Edit(details, input.DeviceID, u.clock.Now()); err != nil {
		return Output{}, err
	}

	if err := u.works.Update(ctx, work); err != nil {
		return Output{}, err
	}

	return Output{Ebook: work}, nil
}

// apply builds the description the write stores: the fields the mask named,
// over the ones the row already carries.
func apply(current *ebook.Details, changes *Changes) (*ebook.Details, error) {
	updated := *current

	if changes.Title != nil {
		title, err := ebook.ParseTitle(*changes.Title)
		if err != nil {
			return nil, err
		}

		updated.Title = title
	}

	if changes.Author != nil {
		author, err := ebook.ParseAuthor(*changes.Author)
		if err != nil {
			return nil, err
		}

		updated.Author = author
	}

	if changes.Publisher != nil {
		publisher, err := ebook.ParsePublisher(*changes.Publisher)
		if err != nil {
			return nil, err
		}

		updated.Publisher = publisher
	}

	if changes.Language != nil {
		language, err := ebook.ParseLanguage(*changes.Language)
		if err != nil {
			return nil, err
		}

		updated.Language = language
	}

	if changes.Extra != nil {
		updated.Extra = ebook.Metadata(*changes.Extra)
	}

	return &updated, nil
}
