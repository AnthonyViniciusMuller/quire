package records

import (
	"context"
	"errors"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	readingprogress "github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// reconcilePosition merges a change to where a device stopped in a work
// (reading.progress).
//
// It is the one record in the node that cannot conflict, and C05 is why: the
// row belongs to one work and one device, so that device is its only writer,
// its writes are totally ordered by its own counter, and two versions of it can
// never be concurrent. The merge here is therefore the causal order alone —
// [crdt.Version.Supersedes] — with no tie-break, because there is nothing to
// tie.
//
// The device is not read from the delta. It is the device that authored the
// operation, which is the only device that may write the row, and taking it
// from anywhere else would let one appliance move another's bookmark. That is
// also the second half of the natural key this record is addressed by (C18):
// the row carries a surrogate identifier each replica mints for itself, so the
// pair is what identifies it, and the delta carries only the work.
//
// There is no deletion. A reader who stops reading a work leaves their position
// where it was, and the row goes when the work goes, by the cascade the schema
// declares — so a change that claimed to remove one is refused rather than
// silently ignored.
func (s *Service) reconcilePosition(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	if op.Kind == operation.KindDelete {
		return rejected(errs.New(errs.KindInvalidArgument, "a reading position cannot be removed").
			WithOp(opReconcile).
			WithCode(operation.CodeInvalidDelta).
			WithField("operation", "a reader who stops reading a work leaves their position where it was"))
	}

	var work uuid.UUID

	if err := assign(op.Delta, fieldEbookID, &work); err != nil {
		return rejected(err)
	}

	if work == (uuid.UUID{}) {
		return rejected(errs.New(errs.KindInvalidArgument, "the position names no work").
			WithOp(opReconcile).
			WithCode(operation.CodeInvalidDelta).
			WithField(fieldEbookID, "a position must say which work it is in"))
	}

	refusal, mine, err := s.workBelongsTo(ctx, work, op.UserID)
	if err != nil {
		return verdict(err)
	}

	if !mine {
		return refusal, nil
	}

	position, err := s.positions.GetByPair(ctx, work, op.DeviceID)

	switch {
	case errors.Is(err, errs.KindNotFound):
		return s.insertPosition(ctx, op, work)
	case err != nil:
		return verdict(err)
	case !op.Version().Supersedes(position.Version):
		return operation.Superseded(), nil
	}

	props := position.Props
	if err = applyPosition(&props.Position, op.Delta); err != nil {
		return rejected(err)
	}

	props.Version = op.Version()

	if err = s.positions.Update(ctx, readingprogress.Restore(position.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// insertPosition records the first position a device reported in a work.
func (s *Service) insertPosition(
	ctx context.Context, op *operation.Operation, work uuid.UUID,
) (operation.Verdict, error) {
	props := readingprogress.Props{
		EbookID:  work,
		DeviceID: op.DeviceID,
		Version:  op.Version(),
	}

	for _, read := range []func() error{
		func() error { return required(op.Delta, fieldLocator, new(string)) },
		func() error { return applyPosition(&props.Position, op.Delta) },
	} {
		if err := read(); err != nil {
			return rejected(err)
		}
	}

	if err := props.Validate(); err != nil {
		return rejected(err)
	}

	if err := s.positions.Create(ctx, readingprogress.Restore(op.Target.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// applyPosition writes the fields the delta claims onto the position.
//
// The two travel together in the entity because they are one reading of one
// moment, and a delta that claims only one of them is a client saying the other
// did not change — which for a position is unusual and not wrong: a client that
// cannot compute a proportion for a format it does not understand still knows
// where the reader is.
func applyPosition(position *readingprogress.Position, delta operation.Delta) error {
	if err := text(delta, fieldLocator, locator.Parse, &position.Locator); err != nil {
		return err
	}

	var claimed *float64

	written, err := claim(delta, fieldPercent, &claimed)
	if err != nil || !written {
		return err
	}

	percent, err := readingprogress.ParsePercent(claimed)
	if err != nil {
		return err
	}

	position.Percent = percent

	return nil
}
