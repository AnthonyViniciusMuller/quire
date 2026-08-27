// Package progress is where one device stopped in one work: the entity, the
// value objects that describe the position, and the port a repository has to
// satisfy.
//
// One row per work and device, as Quadro 21 specifies, and that grain is the
// whole design of the package. A row belongs to one device, so that device is
// its only writer; its writes are totally ordered by its own counter; and two
// versions of it can therefore never be concurrent. Reading progress is
// conflict-free by construction, which is C05 in docs/tcc-corrections.md.
//
// Three things follow, and each of them is visible in the types here rather
// than left to a comment. The row carries a [crdt.Version] and not a
// [crdt.Revision]: the clock is a version counter for deduplication during
// replication, and the two fields that break a tie would be a tie-break for a
// tie that cannot happen. [Progress.MoveTo] takes no device, because the only
// device that may write the row is the one the row already names — an entity
// that accepted one could be asked to move another device's bookmark. And
// there is no tombstone, because a reader who stops reading a work leaves
// their position where it was; the row goes when the work goes, by the cascade
// the schema declares.
//
// Which position to show the reader — the furthest, the most recent, or a
// prompt asking which to resume from — is not decided here and not decided by
// the node. It is the client's decision (RN01), and making it needs every
// device's row, which is what the listing answers with.
package progress

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew    = "reading/progress: new"
	opMoveTo = "reading/progress: move to"
)

// CodeInvalidProgress is a position that could not be recorded for a reason
// none of the value objects owns.
const CodeInvalidProgress = "invalid_reading_progress"

// Position is where the reader stopped, and how far through that is.
//
// The two travel together because they are one reading of one moment: a
// locator from this write and a proportion from the last would describe a
// place the reader has never been.
type Position struct {
	// Locator is the place in the document, and the truth about where the
	// reader is.
	Locator locator.Locator
	// Percent is how far through that is, absent when the client could not
	// compute it.
	Percent Percent
}

// Validate reports why the position is not usable, or nil.
func (p *Position) Validate() error {
	if err := p.Locator.Validate(); err != nil {
		return err
	}

	return p.Percent.Validate()
}

// Props is everything about a position other than its identifier.
type Props struct {
	// EbookID is the work being read, and half of the row's natural key.
	EbookID uuid.UUID

	// DeviceID is the device the position belongs to, and the other half. It
	// is not a tie-break: nothing ever ties. It is the only device that may
	// write the row, which is what makes that true.
	DeviceID uuid.UUID

	// Position is where that device stopped.
	Position

	// Version is the causal state of the row, which every write stamps.
	Version crdt.Version
}

// Progress is where one device stopped in one work (MER: progresso_leitura;
// reading.progress).
type Progress struct {
	// ID is the primary key. The row is addressed by the pair everywhere in
	// this node — a device writes its own position in one work, and reads
	// every device's — so the identifier exists to be the key the operation a
	// write is appended to names, and not to be looked up by.
	ID uuid.UUID

	Props
}

// New records where a device has reached in a work for the first time (UC05).
func New(ebookID, deviceID uuid.UUID, position *Position, at time.Time) (*Progress, error) {
	if err := position.Validate(); err != nil {
		return nil, err
	}

	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the reading position could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidProgress).
			WithField(field, reason)
	}

	switch {
	case ebookID == (uuid.UUID{}):
		return nil, invalid("ebook_id", "a position must name the work it is in")
	case deviceID == (uuid.UUID{}):
		return nil, invalid("device_id", "a position must name the device it belongs to")
	case at.IsZero():
		return nil, invalid("updated_at", "a position must say when it was reached")
	}

	return &Progress{
		ID: uuid.New(),
		Props: Props{
			EbookID:  ebookID,
			DeviceID: deviceID,
			Position: *position,
			Version:  crdt.FirstVersion(deviceID, at),
		},
	}, nil
}

// Restore rebuilds a position already stored.
func Restore(id uuid.UUID, props *Props) *Progress {
	return &Progress{ID: id, Props: *props}
}

// BelongsTo reports whether the position is deviceID's.
func (p *Progress) BelongsTo(deviceID uuid.UUID) bool { return p.DeviceID == deviceID }

// MoveTo records where the device has reached now, and stamps the write.
//
// It takes no device. The row has one writer and it is the one the row names,
// so the device is read from the entity rather than accepted from the caller —
// which is what makes RN10 a property of the type here instead of a check
// somebody has to remember. A request that could name a device would be a
// request that could move another device's bookmark, and the contract says so
// by leaving the field out of UpdateReadingProgressRequest.
//
// A move to the position the row already holds is stamped like any other. The
// reader has read on, whatever the locator says, and a write that recorded
// nothing would leave a replica believing this device had stopped where it had
// not.
func (p *Progress) MoveTo(position *Position, at time.Time) error {
	if err := position.Validate(); err != nil {
		return err
	}

	if at.IsZero() {
		return errs.New(errs.KindInvalidArgument, "the reading position could not be moved").
			WithOp(opMoveTo).
			WithCode(CodeInvalidProgress).
			WithField("updated_at", "a position must say when it was reached")
	}

	p.Position = *position
	p.Version = p.Version.Next(p.DeviceID, at)

	return nil
}
