package records

import (
	"context"
	"errors"
	"time"

	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// reconcileEbook merges a change to a work (library.ebooks).
//
// The work is addressed by the identifier the operation names, and that
// identifier travels: a work is created once, by one device, and what it minted
// is what every mark and every position references. Nothing here mints another.
func (s *Service) reconcileEbook(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	work, err := s.works.GetByID(ctx, op.Target.ID)

	switch {
	case errors.Is(err, errs.KindNotFound):
		if op.Kind != operation.KindInsert {
			return missing(op.Target.Entity)
		}

		return s.insertEbook(ctx, op)
	case err != nil:
		return verdict(err)
	case !work.BelongsTo(op.UserID):
		return notMine(op.Target.Entity)
	case !op.Revision().Supersedes(work.Revision):
		return operation.Superseded(), nil
	}

	props := work.Props
	if err = applyEbookDetails(&props.Details, op.Delta); err != nil {
		return rejected(err)
	}

	props.Revision = op.Revision()

	if err = s.works.Update(ctx, libraryebook.Restore(work.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// insertEbook records a work this node has not seen.
//
// The entity is restored rather than constructed, because constructing one
// mints an identifier and the identifier is the author's. What the constructor
// would have checked is checked here instead, by parsing every value through
// the type that owns it and then validating the pair of structures it built:
// a title the column cannot hold is refused by the domain and reported against
// the field that carried it, rather than reaching the constraint and being
// reported as a table.
func (s *Service) insertEbook(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	props := libraryebook.Props{UserID: op.UserID, Revision: op.Revision()}

	// A work that did not say when it entered the collection entered it when
	// the change that created it was authored. The column is NOT NULL and the
	// instant is a wall clock a reader is shown, so an absent one has to be
	// something rather than nothing.
	props.ImportedAt = op.CreatedAt

	var importedAt *time.Time

	for _, read := range []func() error{
		func() error { return applyEbookDetails(&props.Details, op.Delta) },
		func() error { return applyEbookFile(&props.File, op.Delta) },
		func() error { return assign(op.Delta, fieldImportedAt, &importedAt) },
	} {
		if err := read(); err != nil {
			return rejected(err)
		}
	}

	if importedAt != nil {
		props.ImportedAt = importedAt.UTC()
	}

	if err := props.Details.Validate(); err != nil {
		return rejected(err)
	}

	if err := props.File.Validate(); err != nil {
		return rejected(err)
	}

	if err := s.works.Create(ctx, libraryebook.Restore(op.Target.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// applyEbookDetails writes the fields the delta claims onto the description,
// and leaves every field it does not claim as it was.
//
// The five are exactly the paths UpdateEbook admits, and that is not a
// coincidence: an operation is what a device wrote, and a device writes through
// the same contract.
func applyEbookDetails(details *libraryebook.Details, delta operation.Delta) error {
	for _, apply := range []func() error{
		func() error { return text(delta, fieldTitle, libraryebook.ParseTitle, &details.Title) },
		func() error { return text(delta, fieldAuthor, libraryebook.ParseAuthor, &details.Author) },
		func() error { return text(delta, fieldPublisher, libraryebook.ParsePublisher, &details.Publisher) },
		func() error { return text(delta, fieldLanguage, libraryebook.ParseLanguage, &details.Language) },
		func() error { return assign(delta, fieldExtra, &details.Extra) },
	} {
		if err := apply(); err != nil {
			return err
		}
	}

	return nil
}

// applyEbookFile writes what the bytes are, which only an insert carries.
//
// It is not among the fields an update may write, for the reason the library's
// own repository gives: the format, the digest and the length are fixed at
// import, and a change that moved them would make the row describe a file it is
// not.
func applyEbookFile(file *libraryebook.File, delta operation.Delta) error {
	if err := required(delta, fieldFormat, new(string)); err != nil {
		return err
	}

	if err := required(delta, fieldContentHash, new(string)); err != nil {
		return err
	}

	var size *int64

	for _, apply := range []func() error{
		func() error { return text(delta, fieldFormat, libraryebook.ParseFormat, &file.Format) },
		func() error { return text(delta, fieldContentHash, libraryebook.ParseContentHash, &file.Hash) },
		func() error { return assign(delta, fieldSizeBytes, &size) },
	} {
		if err := apply(); err != nil {
			return err
		}
	}

	file.Size = libraryebook.Size(value(size))

	return nil
}
