// Package annotation is something the reader wrote in a work: the entity, the
// value objects that describe it, and the port a repository has to satisfy.
//
// UC04 is marked «CRUD», and the U is what shapes the package. An annotation
// can be edited, and it can be edited from a device other than the one that
// made it — a note started on a phone and finished on a tablet is the ordinary
// case, not the corner one. That is why the row carries a full
// [crdt.Revision]: two devices can hold concurrent versions of the same mark,
// so settling them needs the causal clock and, when that reports concurrency,
// the tie-break the timestamp and the device make. It is also why
// Revision.DeviceID means the device whose write the row currently reflects
// rather than the one that created it, which is C10 in
// docs/tcc-corrections.md.
//
// It is the other half of a distinction this slice is built around. Reading
// progress is written by one device and can never conflict; an annotation is
// written by all of them and regularly does. The two entities of one slice
// reconcile differently, and each carries exactly the metadata its own
// reconciliation needs (C05).
//
// The reader is not named here. reading.annotations references the work and
// not the reader, so who an annotation belongs to is a question about the work
// it is attached to — which is what the use cases ask, of the library slice's
// repository, before they answer anything about a mark.
//
// That is also why deleting a work does not tombstone what was written in it.
// The work's tombstone already makes every mark in it unreachable, since every
// call here establishes the reader through the work and a tombstoned work is
// answered as one that does not exist; and the alternative — the library slice
// writing rows this one owns — would be the first time a slice reached into
// another's tables to finish its own transaction.
package annotation

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew  = "reading/annotation: new"
	opEdit = "reading/annotation: edit"
)

// CodeInvalidAnnotation is a mark that could not be recorded for a reason none
// of the value objects owns.
const CodeInvalidAnnotation = "invalid_annotation"

// Mark is what the reader left, and where.
//
// The three fields move together because all three are editable and the row
// carries one revision: a write that changes the text of a highlight and a
// write that turns it into a note are the same kind of event, and separating
// them would suggest the row could be at two versions at once.
type Mark struct {
	// Kind is what kind of mark it is.
	Kind Kind
	// Text is what the reader wrote, absent on a highlight or a bookmark they
	// left uncommented.
	Text Text
	// Locator is the passage it is attached to.
	Locator locator.Locator
}

// Validate reports why the mark is not usable, or nil.
//
// The last check is the one that is about the pair rather than about either
// field: reading.annotations_note_has_text refuses a note with nothing in it,
// because a note is the text — a mark with no text that is not attached to a
// passage the reader wanted remembered is a highlight or a bookmark, and the
// contract has both.
func (m *Mark) Validate() error {
	for _, validate := range []func() error{
		m.Kind.Validate,
		m.Text.Validate,
		m.Locator.Validate,
	} {
		if err := validate(); err != nil {
			return err
		}
	}

	if m.Kind == KindNote && m.Text.IsBlank() {
		return emptyNote()
	}

	return nil
}

// Props is everything about a mark other than its identifier.
type Props struct {
	// EbookID is the work the mark is in. It is what every read is scoped by,
	// and what the reader it belongs to is established through.
	EbookID uuid.UUID

	// Mark is what the reader left.
	Mark

	// Revision is the causal state of the row, which every write stamps.
	Revision crdt.Revision
}

// Annotation is one mark a reader left in a work (MER: anotacao;
// reading.annotations).
type Annotation struct {
	// ID is what the reply carries and what an edit addresses. It is minted
	// here rather than by the column default, because the operation the write
	// is appended to names it and because a client that has just created a
	// mark has to be able to edit it without listing the work again.
	ID uuid.UUID

	Props
}

// New records a mark a device has just made (UC04, create).
//
// The device is a parameter and not a detail: it keys the first vector clock
// entry, so a mark made by an appliance that was never bound would introduce a
// causal history no node could attribute to anybody (RN10).
func New(ebookID uuid.UUID, mark *Mark, device uuid.UUID, at time.Time) (*Annotation, error) {
	if err := mark.Validate(); err != nil {
		return nil, err
	}

	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the mark could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidAnnotation).
			WithField(field, reason)
	}

	switch {
	case ebookID == (uuid.UUID{}):
		return nil, invalid("ebook_id", "a mark must name the work it is in")
	case device == (uuid.UUID{}):
		return nil, invalid("device_id", "a mark must name the device that made it")
	case at.IsZero():
		return nil, invalid("updated_at", "a mark must say when it was made")
	}

	return &Annotation{
		ID: uuid.New(),
		Props: Props{
			EbookID:  ebookID,
			Mark:     *mark,
			Revision: crdt.FirstRevision(device, at),
		},
	}, nil
}

// Restore rebuilds a mark already stored, without minting an identifier: the
// id is the one the client holds and the one an operation names.
func Restore(id uuid.UUID, props *Props) *Annotation {
	return &Annotation{ID: id, Props: *props}
}

// IsIn reports whether the mark is in ebookID.
//
// Every call that names a mark has to make this check, because the identifier
// alone says nothing about whose it is: the reader is established through the
// work, so a mark read by its own identifier has to be confirmed to be in the
// work whose owner was checked.
func (a *Annotation) IsIn(ebookID uuid.UUID) bool { return a.EbookID == ebookID }

// Edit replaces the mark and stamps the write (UC04, update; RF03).
//
// It takes the whole mark rather than the fields that changed, and the caller
// is the one that applied the field mask to build it. The row carries one
// revision and not one per column, so a write that claims two fields is one
// version of the record — and an entity that accepted partial marks would have
// to decide what an omitted field means, which is a decision the mask already
// made.
//
// The device is the one making this write and not the one that made the mark.
// After an edit from a second device the two differ, and the tie-break needs
// the device whose write the row reflects (C10).
func (a *Annotation) Edit(mark *Mark, device uuid.UUID, at time.Time) error {
	if err := mark.Validate(); err != nil {
		return err
	}

	if device == (uuid.UUID{}) {
		return errs.New(errs.KindInvalidArgument, "the mark could not be edited").
			WithOp(opEdit).
			WithCode(CodeInvalidAnnotation).
			WithField("device_id", "an edit must name the device that made it")
	}

	a.Mark = *mark
	a.Revision = a.Revision.Next(device, at)

	return nil
}

// Delete tombstones the mark.
//
// The row stays and the tombstone travels. A mark removed outright is
// resurrected by the next node that had not yet heard about the deletion,
// which on a reader with a phone that has been offline for a week is not a
// corner case but the ordinary one.
func (a *Annotation) Delete(device uuid.UUID, at time.Time) {
	a.Revision = a.Revision.Tombstone(device, at)
}

// IsDeleted reports whether the mark has been tombstoned.
func (a *Annotation) IsDeleted() bool { return a.Revision.Deleted }
