package records

import (
	"context"
	"errors"
	"uuid"

	librarymembership "github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// reconcileFiling merges a change to the filing of a work under a grouping
// (library.ebook_collections).
//
// It is the first of the two records addressed by their natural key rather
// than by the identifier the operation names, and C18 in
// docs/tcc-corrections.md is why: the row carries a surrogate key each replica
// mints for itself, so two devices filing the same work under the same shelf
// while offline produce two identifiers for one record. The pair is what the
// schema makes unique (C06) and it is what identifies the record across the
// federation, so the delta has to carry it — which costs nothing, since the
// device writing the filing holds both halves by definition.
//
// There is no insert and no update here, only the register: filing a work and
// filing it again are the same request, and the row is reused with its
// tombstone flipped rather than written twice. What the kind of change decides
// is which way the register goes.
func (s *Service) reconcileFiling(
	ctx context.Context, op *operation.Operation,
) (operation.Verdict, error) {
	var work, grouping uuid.UUID

	for _, read := range []func() error{
		func() error { return assign(op.Delta, fieldEbookID, &work) },
		func() error { return assign(op.Delta, fieldCollectionID, &grouping) },
	} {
		if err := read(); err != nil {
			return rejected(err)
		}
	}

	if work == (uuid.UUID{}) || grouping == (uuid.UUID{}) {
		return rejected(errs.New(errs.KindInvalidArgument, "the filing names no pair").
			WithOp(opReconcile).
			WithCode(operation.CodeInvalidDelta).
			WithField(fieldEbookID, "a filing is identified by the work and the grouping, and must carry both"))
	}

	// The work is what the pair is checked against. The filing itself names no
	// reader, so a peer offering one for a work that is not the reader's would
	// otherwise reach another reader's collection through an authorization
	// that is not theirs.
	refusal, mine, err := s.workBelongsTo(ctx, work, op.UserID)
	if err != nil {
		return verdict(err)
	}

	if !mine {
		return refusal, nil
	}

	filing, err := s.filings.GetByPair(ctx, work, grouping)

	switch {
	case errors.Is(err, errs.KindNotFound):
		return s.insertFiling(ctx, op, work, grouping)
	case err != nil:
		return verdict(err)
	case !op.Revision().Supersedes(filing.Revision):
		return operation.Superseded(), nil
	}

	props := filing.Props
	props.Revision = op.Revision()

	if err = s.filings.Update(ctx, librarymembership.Restore(filing.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}

// insertFiling writes a pair this node has never held.
//
// A deletion of a pair that was never written is written too, tombstone and
// all, and that is not a waste. The row is what carries the causal state: a
// node that dropped the tombstone would file the work again the moment it
// heard about the filing this deletion undid, which is exactly the
// resurrection a tombstone exists to prevent.
func (s *Service) insertFiling(
	ctx context.Context, op *operation.Operation, work, grouping uuid.UUID,
) (operation.Verdict, error) {
	props := librarymembership.Props{
		EbookID:      work,
		CollectionID: grouping,
		Revision:     op.Revision(),
	}

	if err := s.filings.Create(ctx, librarymembership.Restore(op.Target.ID, &props)); err != nil {
		return verdict(err)
	}

	return operation.Applied(), nil
}
