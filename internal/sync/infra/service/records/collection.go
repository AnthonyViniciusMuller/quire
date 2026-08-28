package records

import (
	"context"
	"errors"
	"time"

	librarycollection "github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// reconcileCollection merges a change to a grouping (library.collections).
//
// It is the work's reconciliation with a smaller description, and the shape is
// the same for the same reason: a grouping is defined once, by one device, and
// the identifier it minted is what every filing references.
func (s *Service) reconcileCollection(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	grouping, err := s.groupings.GetByID(ctx, op.Target.ID)

	switch {
	case errors.Is(err, errs.KindNotFound):
		if op.Kind != operation.KindInsert {
			return missing(op.Target.Entity)
		}

		return s.insertCollection(ctx, op)
	case err != nil:
		return verdict(err)
	case !grouping.BelongsTo(op.UserID):
		return notMine(op.Target.Entity)
	case !op.Revision().Supersedes(grouping.Revision):
		return operation.Superseded(), nil
	}

	props := grouping.Props
	if err = applyCollectionDetails(&props.Details, op.Delta); err != nil {
		return rejected(err)
	}

	props.Revision = op.Revision()

	if err = s.groupings.Update(ctx, librarycollection.Restore(grouping.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// insertCollection records a grouping this node has not seen.
func (s *Service) insertCollection(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	props := librarycollection.Props{
		UserID:    op.UserID,
		CreatedAt: op.CreatedAt,
		Revision:  op.Revision(),
	}

	if err := required(op.Delta, fieldName, new(string)); err != nil {
		return rejected(err)
	}

	var createdAt *time.Time

	for _, read := range []func() error{
		func() error { return applyCollectionDetails(&props.Details, op.Delta) },
		func() error { return assign(op.Delta, fieldCreatedAt, &createdAt) },
	} {
		if err := read(); err != nil {
			return rejected(err)
		}
	}

	if createdAt != nil {
		props.CreatedAt = createdAt.UTC()
	}

	if err := props.Validate(); err != nil {
		return rejected(err)
	}

	if err := s.groupings.Create(ctx, librarycollection.Restore(op.Target.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// applyCollectionDetails writes the fields the delta claims onto the grouping.
//
// The kind is claimable, as UpdateCollection admits it: a reader who decides
// that what they made is a subject rather than a shelf is changing the
// grouping and not replacing it.
func applyCollectionDetails(details *librarycollection.Details, delta operation.Delta) error {
	for _, apply := range []func() error{
		func() error { return text(delta, fieldName, librarycollection.ParseName, &details.Name) },
		func() error { return text(delta, fieldKind, librarycollection.ParseKind, &details.Kind) },
		func() error {
			return text(delta, fieldDescription, librarycollection.ParseDescription, &details.Description)
		},
	} {
		if err := apply(); err != nil {
			return err
		}
	}

	return nil
}
