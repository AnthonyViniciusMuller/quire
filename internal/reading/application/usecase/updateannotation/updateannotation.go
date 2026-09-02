// Package updateannotation is the update half of UC04 (RF03).
//
// It is the half that makes an annotation need a full revision. A mark can be
// edited from a device other than the one that made it, so two versions of it
// can be concurrent — and the device the row names afterwards is the one whose
// write it reflects, which is C10 in docs/tcc-corrections.md.
package updateannotation

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/application/usecase/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "reading/updateannotation: execute"

// CodeEmptyUpdate is an edit that claims no field at all.
const CodeEmptyUpdate = "empty_update"

// UpdateAnnotation edits marks.
type UpdateAnnotation struct {
	marks annotation.Repository
	works service.Works
	clock service.Clock
}

// UpdateAnnotation satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*UpdateAnnotation)(nil)

// New returns the use case over its dependencies.
func New(marks annotation.Repository, works service.Works, clock service.Clock) *UpdateAnnotation {
	return &UpdateAnnotation{marks: marks, works: works, clock: clock}
}

// Execute applies the fields the mask named and stamps the write.
//
// An edit that claims nothing is refused rather than served as a no-op. It
// would not be a no-op: it would stamp a revision, and a version that claims a
// write nobody made would win a tie-break against a real edit from a device
// that had been offline.
//
// The read and the write are not wrapped in a transaction, and the reason is
// the library slice's: two devices editing the same mark at the same moment is
// exactly what the vector clock exists to resolve, and it resolves it whether
// or not the two writes were serialized here — the loser's write is a version,
// not a lost update.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (u *UpdateAnnotation) Execute(ctx context.Context, input Input) (Output, error) {
	if input.Changes.IsEmpty() {
		return Output{}, errs.New(errs.KindInvalidArgument, "the edit claims no field").
			WithOp(opExecute).
			WithCode(CodeEmptyUpdate).
			WithField("update_mask", "it must name at least one field to write")
	}

	mark, err := u.marks.GetByID(ctx, input.AnnotationID)
	if err != nil {
		return Output{}, err
	}

	if mark.IsDeleted() {
		return Output{}, getannotation.NotFound(opExecute)
	}

	if err = u.works.Visible(ctx, mark.EbookID, input.UserID); err != nil {
		return Output{}, getannotation.NotVisible(opExecute, err)
	}

	edited, err := apply(&mark.Mark, &input.Changes)
	if err != nil {
		return Output{}, err
	}

	if err = mark.Edit(edited, input.DeviceID, u.clock.Now()); err != nil {
		return Output{}, err
	}

	if err = u.marks.Update(ctx, mark); err != nil {
		return Output{}, err
	}

	return Output{Annotation: mark}, nil
}

// apply builds the mark the write stores: the fields the mask named, over the
// ones the row already carries.
//
// The note rule is checked against the result and not against the claim, which
// is what makes clearing the text of a note a refusal rather than an accepted
// write the row rejects: a mask naming only the text, on a row whose kind is a
// note, has to be read together with that kind.
func apply(current *annotation.Mark, changes *Changes) (*annotation.Mark, error) {
	updated := *current

	if changes.Kind != nil {
		kind, err := annotation.ParseKind(*changes.Kind)
		if err != nil {
			return nil, err
		}

		updated.Kind = kind
	}

	if changes.Text != nil {
		updated.Text = annotation.ParseText(*changes.Text)
	}

	if changes.Locator != nil {
		place, err := locator.Parse(*changes.Locator)
		if err != nil {
			return nil, err
		}

		updated.Locator = place
	}

	return &updated, nil
}
