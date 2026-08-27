// Package collection is a grouping a reader defined over their works: the
// entity, the value objects that describe it, and the port a repository has to
// satisfy.
//
// A collection and a category are the same structure with a different meaning,
// which is what lets RF05 offer both without a second entity (UC03). Nothing
// in the node branches on which one a row is; the distinction exists so that a
// client can present a shelf and a subject differently.
//
// What is filed under a grouping is not here. That is
// internal/library/domain/membership, which is its own entity because it
// replicates on its own terms: two devices that add and remove the same work
// from the same shelf while disconnected have to reach the same answer, and a
// list held inside this entity could not carry the causal state that decides
// it.
//
// Deleting a grouping does not delete what was on it. The works survive their
// shelf, and the memberships are tombstoned with it by the caller.
package collection

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew  = "library/collection: new"
	opEdit = "library/collection: edit"
)

// CodeInvalidCollection is a grouping that could not be recorded for a reason
// none of the value objects owns.
const CodeInvalidCollection = "invalid_collection"

// Details is everything about a grouping the reader may write.
type Details struct {
	// Name is what they call it.
	Name Name
	// Kind is whether it is a shelf or a subject.
	Kind Kind
	// Description is what they wrote about it, absent when they wrote nothing.
	Description Description
}

// Validate reports why the description is not usable, or nil.
func (d *Details) Validate() error {
	for _, validate := range []func() error{
		d.Name.Validate,
		d.Kind.Validate,
		d.Description.Validate,
	} {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

// Props is everything about a grouping other than its identifier.
type Props struct {
	// UserID is the reader who defined it. Every read checks it: a grouping
	// that belongs to somebody else is answered exactly as one that does not
	// exist.
	UserID uuid.UUID

	// Details is what the reader may write.
	Details

	// CreatedAt is when the grouping was defined. It is a wall clock and not
	// the replication timestamp, for the reason a work's import instant is
	// one.
	CreatedAt time.Time

	// Revision is the causal state of the row, which every write stamps.
	Revision crdt.Revision
}

// Collection is one grouping a reader defined (MER: colecao;
// library.collections).
type Collection struct {
	// ID is what a membership references.
	ID uuid.UUID

	Props
}

// New defines a grouping (UC03).
//
// The device is a parameter for the reason it is one on a work: it keys the
// first vector clock entry and it is what the tie-break names.
func New(userID uuid.UUID, details *Details, device uuid.UUID, at time.Time) (*Collection, error) {
	if err := details.Validate(); err != nil {
		return nil, err
	}

	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the grouping could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidCollection).
			WithField(field, reason)
	}

	switch {
	case userID == (uuid.UUID{}):
		return nil, invalid("user_id", "a grouping must name the reader who defined it")
	case device == (uuid.UUID{}):
		return nil, invalid("device_id", "a grouping must name the device that defined it")
	case at.IsZero():
		return nil, invalid("created_at", "a grouping must say when it was defined")
	}

	return &Collection{
		ID: uuid.New(),
		Props: Props{
			UserID:    userID,
			Details:   *details,
			CreatedAt: at.UTC().Truncate(crdt.Resolution),
			Revision:  crdt.FirstRevision(device, at),
		},
	}, nil
}

// Restore rebuilds a grouping already stored, without minting an identifier:
// the id is the one every membership references.
func Restore(id uuid.UUID, props *Props) *Collection {
	return &Collection{ID: id, Props: *props}
}

// BelongsTo reports whether the grouping is userID's.
func (c *Collection) BelongsTo(userID uuid.UUID) bool { return c.UserID == userID }

// Edit replaces the description and stamps the write.
//
// It takes the whole description rather than the fields that changed, for the
// reason a work's edit does: the row carries one revision, and the field mask
// was applied by the caller that built this.
func (c *Collection) Edit(details *Details, device uuid.UUID, at time.Time) error {
	if err := details.Validate(); err != nil {
		return err
	}

	if device == (uuid.UUID{}) {
		return errs.New(errs.KindInvalidArgument, "the grouping could not be edited").
			WithOp(opEdit).
			WithCode(CodeInvalidCollection).
			WithField("device_id", "an edit must name the device that made it")
	}

	c.Details = *details
	c.Revision = c.Revision.Next(device, at)

	return nil
}

// Delete tombstones the grouping. The works survive it; their memberships are
// tombstoned by the caller in the same transaction.
func (c *Collection) Delete(device uuid.UUID, at time.Time) {
	c.Revision = c.Revision.Tombstone(device, at)
}

// IsDeleted reports whether the grouping has been tombstoned.
func (c *Collection) IsDeleted() bool { return c.Revision.Deleted }
