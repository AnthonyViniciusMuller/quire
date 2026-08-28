package records

import (
	"context"
	"errors"
	"uuid"

	readingannotation "github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// reconcileAnnotation merges a change to a mark (reading.annotations).
//
// A mark is the entity this whole mechanism was written for. It is edited from
// devices other than the one that made it — a note started on a phone and
// finished on a tablet is the ordinary case — so it carries the full revision
// and its concurrent versions really do have to be settled on the tie-break.
//
// It names no reader. reading.annotations references the work, so whose a mark
// is is a fact about the work it is in, and that is the check made here as it
// is made by every use case of the reading slice.
func (s *Service) reconcileAnnotation(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	mark, err := s.marks.GetByID(ctx, op.Target.ID)

	switch {
	case errors.Is(err, errs.KindNotFound):
		if op.Kind != operation.KindInsert {
			return missing(op.Target.Entity)
		}

		return s.insertAnnotation(ctx, op)
	case err != nil:
		return verdict(err)
	case !op.Revision().Supersedes(mark.Revision):
		return operation.Superseded(), nil
	}

	refusal, mine, err := s.workBelongsTo(ctx, mark.EbookID, op.UserID)
	if err != nil {
		return verdict(err)
	}

	if !mine {
		return refusal, nil
	}

	props := mark.Props
	if err = applyMark(&props.Mark, op.Delta); err != nil {
		return rejected(err)
	}

	props.Revision = op.Revision()

	if err = s.marks.Update(ctx, readingannotation.Restore(mark.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// insertAnnotation records a mark this node has not seen.
//
// The work it is in has to be named, and has to be the reader's: the row
// references library.ebooks and nothing else, so a mark accepted for somebody
// else's work would be a mark nobody could ever read and a foreign key nobody
// checked.
func (s *Service) insertAnnotation(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	var work uuid.UUID

	if err := assign(op.Delta, fieldEbookID, &work); err != nil {
		return rejected(err)
	}

	if work == (uuid.UUID{}) {
		return rejected(errs.New(errs.KindInvalidArgument, "the mark names no work").
			WithOp(opReconcile).
			WithCode(operation.CodeInvalidDelta).
			WithField(fieldEbookID, "a mark must say which work it is in"))
	}

	refusal, mine, err := s.workBelongsTo(ctx, work, op.UserID)
	if err != nil {
		return verdict(err)
	}

	if !mine {
		return refusal, nil
	}

	props := readingannotation.Props{EbookID: work, Revision: op.Revision()}

	for _, read := range []func() error{
		func() error { return required(op.Delta, fieldKind, new(string)) },
		func() error { return required(op.Delta, fieldLocator, new(string)) },
		func() error { return applyMark(&props.Mark, op.Delta) },
	} {
		if err = read(); err != nil {
			return rejected(err)
		}
	}

	if err = props.Validate(); err != nil {
		return rejected(err)
	}

	if err = s.marks.Create(ctx, readingannotation.Restore(op.Target.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// applyMark writes the fields the delta claims onto the mark.
//
// The work is not among them. A mark is made in a work and stays in it; a note
// that moved between books would be a note about a passage that is not there,
// and the library's own statement does not write the column either.
func applyMark(mark *readingannotation.Mark, delta operation.Delta) error {
	for _, apply := range []func() error{
		func() error { return text(delta, fieldKind, readingannotation.ParseKind, &mark.Kind) },
		func() error { return text(delta, fieldLocator, locator.Parse, &mark.Locator) },
		func() error {
			var claimed *string

			written, err := claim(delta, fieldText, &claimed)
			if err != nil || !written {
				return err
			}

			mark.Text = readingannotation.ParseText(value(claimed))

			return nil
		},
	} {
		if err := apply(); err != nil {
			return err
		}
	}

	return nil
}

// workBelongsTo reports whether a work is the reader's, and the refusal to
// answer with when it is not.
//
// The two refusals are one answer, as they are everywhere else in this node: a
// work that is not there and a work that is somebody else's are the same fact
// to whoever asked, and telling them apart would be an oracle for which
// identifiers exist.
func (s *Service) workBelongsTo(
	ctx context.Context, work, userID uuid.UUID,
) (operation.Verdict, bool, error) {
	held, err := s.works.GetByID(ctx, work)
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			refusal, _ := missing(operation.TargetEbook)

			return refusal, false, nil
		}

		return operation.Verdict{}, false, err
	}

	if !held.BelongsTo(userID) {
		refusal, _ := notMine(operation.TargetEbook)

		return refusal, false, nil
	}

	return operation.Verdict{}, true, nil
}
