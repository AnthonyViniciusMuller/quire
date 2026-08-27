// Package membership is the filing of one work under one grouping: the entity,
// and the port a repository has to satisfy.
//
// It is an entity and not a list held inside a collection, because it
// replicates on its own terms. Two devices that add and remove the same work
// from the same shelf while disconnected have to reach one answer when they
// meet, and that answer is decided by the causal state of this row — which a
// list inside another entity could not carry.
//
// The row is therefore a register that is set and cleared, never a row that is
// appended and removed. C06 in docs/tcc-corrections.md is why: Quadro 20 has
// no uniqueness constraint, so nothing in the specification stops the same
// work from being filed twice in the same grouping, which is exactly what two
// offline devices will do. The pair is the natural key, and filing a work that
// is already filed flips a tombstone rather than inserting a second row.
//
// That is also what makes both of the contract's calls idempotent, which they
// say they are: adding twice is one register set twice, and removing something
// that was never there is a register cleared that was already clear.
package membership

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by the constructor.
const opNew = "library/membership: new"

// CodeInvalidMembership is a filing that names no work, no grouping or no
// device.
const CodeInvalidMembership = "invalid_membership"

// Props is everything about a filing other than its identifier.
type Props struct {
	// EbookID is the work that was filed.
	EbookID uuid.UUID
	// CollectionID is the grouping it was filed under.
	CollectionID uuid.UUID
	// Revision is the causal state of the register, and its tombstone is what
	// "not filed" means.
	Revision crdt.Revision
}

// Membership is one work filed under one grouping (MER: ebook_colecao;
// library.ebook_collections).
type Membership struct {
	// ID is the primary key. One row per (work, grouping) pair, reused as the
	// register is set and cleared, so that the whole history of the pair stays
	// in one place.
	ID uuid.UUID

	Props
}

// New files a work under a grouping for the first time.
func New(ebookID, collectionID, device uuid.UUID, at time.Time) (*Membership, error) {
	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the work could not be filed").
			WithOp(opNew).
			WithCode(CodeInvalidMembership).
			WithField(field, reason)
	}

	switch {
	case ebookID == (uuid.UUID{}):
		return nil, invalid("ebook_id", "a filing must name the work")
	case collectionID == (uuid.UUID{}):
		return nil, invalid("collection_id", "a filing must name the grouping")
	case device == (uuid.UUID{}):
		return nil, invalid("device_id", "a filing must name the device that made it")
	case at.IsZero():
		return nil, invalid("updated_at", "a filing must say when it was made")
	}

	return &Membership{
		ID: uuid.New(),
		Props: Props{
			EbookID:      ebookID,
			CollectionID: collectionID,
			Revision:     crdt.FirstRevision(device, at),
		},
	}, nil
}

// Restore rebuilds a filing already stored.
func Restore(id uuid.UUID, props *Props) *Membership {
	return &Membership{ID: id, Props: *props}
}

// Set files the work, whether or not it was filed before, and reports whether
// anything changed.
//
// A register that was already set is stamped again rather than left alone. The
// call is idempotent to the reader — the answer is the same either way — but
// it is not a no-op to replication: the write happened on this device, and a
// version that did not record it would let an older removal from another
// device win the tie-break it should have lost.
func (m *Membership) Set(device uuid.UUID, at time.Time) bool {
	filed := !m.Revision.Deleted
	m.Revision = m.Revision.Restore(device, at)

	return !filed
}

// Clear unfiles the work, and reports whether anything changed. It stamps a
// register that was already clear for the reason Set stamps one that was
// already set.
func (m *Membership) Clear(device uuid.UUID, at time.Time) bool {
	filed := !m.Revision.Deleted
	m.Revision = m.Revision.Tombstone(device, at)

	return filed
}

// IsFiled reports whether the work is currently under the grouping.
func (m *Membership) IsFiled() bool { return !m.Revision.Deleted }
