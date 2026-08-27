// Package ebook is a work in a reader's collection: the entity, the value
// objects that describe it, and the port a repository has to satisfy.
//
// An e-book is two things held in one row, and keeping them apart is what the
// rest of this package is arranged around.
//
// [Details] is what a reader may correct — the title, the author, the
// publisher, the language, and whatever else the format carried (RF05). UC01
// is marked «CRD» because the file cannot be edited; its description can, and
// that is the whole of the update this slice serves.
//
// [File] is what the bytes are — the container, the digest and the length. It
// is fixed at import, because a row whose format or digest changed would
// describe a file it is not. Replacing the file means importing another work.
//
// Everything here replicates, so the entity carries a [crdt.Revision] and
// every change to it goes through a method that stamps one. That is what makes
// a write arriving over this service and the same write arriving later as a
// synchronization operation indistinguishable afterwards, which is the
// property the whole offline-first design rests on.
package ebook

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew  = "library/ebook: new"
	opEdit = "library/ebook: edit"
)

// CodeInvalidEbook is a work that could not be recorded for a reason none of
// the value objects owns.
const CodeInvalidEbook = "invalid_ebook"

// Details is everything about a work a reader may correct.
type Details struct {
	// Title is what the work is called, and the only one of these that is
	// required.
	Title Title
	// Author is who wrote it, absent when the file did not say.
	Author Author
	// Publisher is who issued it, absent for the same reason.
	Publisher Publisher
	// Language is the tag the file declares its text in.
	Language Language
	// Extra is the metadata the format carried and this contract does not
	// name (RF05).
	Extra Metadata
}

// Validate reports why the description is not usable, or nil.
func (d *Details) Validate() error {
	for _, validate := range []func() error{
		d.Title.Validate,
		d.Author.Validate,
		d.Publisher.Validate,
		d.Language.Validate,
	} {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

// File is what the bytes of a work are, as the row records them.
//
// The digest is not a reference to anything this node holds. Whether the bytes
// are here is a separate question, asked of library.ebook_contents, and a node
// replicating a reader without their files answers it with no for every row it
// has (D02).
type File struct {
	// Format is the container.
	Format Format
	// Hash is the digest of the bytes, and the name they are stored under.
	Hash ContentHash
	// Size is the length in bytes, absent when the client declared none.
	Size Size
}

// Validate reports why the file is not usable, or nil.
func (f *File) Validate() error {
	for _, validate := range []func() error{
		f.Format.Validate,
		f.Hash.Validate,
		f.Size.Validate,
	} {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

// Props is everything about a work other than its identifier.
type Props struct {
	// UserID is the reader whose collection the work is in. Every read checks
	// it: a work that belongs to somebody else is answered exactly as one that
	// does not exist.
	UserID uuid.UUID

	// Details is what the reader may correct.
	Details

	// File is what the bytes are, fixed at import.
	File

	// ImportedAt is when the work entered the collection. It is a wall clock
	// and not the replication timestamp: it says when this happened, which is
	// something a reader is shown, while crdt.Revision.UpdatedAt orders writes
	// and is never rendered as a time of day.
	ImportedAt time.Time

	// Revision is the causal state of the row, which every write stamps.
	Revision crdt.Revision
}

// Ebook is one work in a reader's collection (MER: ebook; library.ebooks).
type Ebook struct {
	// ID is what a membership, an annotation and a reading position all
	// reference. It is minted here rather than by the column default, because
	// the reply that reports the work has to carry it and because the
	// operation the write is appended to names it.
	ID uuid.UUID

	Props
}

// New records a work a device has just imported (UC01, UC02).
//
// The device is a parameter and not a detail: it is what the first vector
// clock entry is keyed by and what the tie-break names, so a work imported by
// an appliance that was never bound would introduce a causal history no node
// could attribute to anybody (RN10).
func New(
	userID uuid.UUID,
	details *Details,
	file *File,
	device uuid.UUID,
	at time.Time,
) (*Ebook, error) {
	if err := details.Validate(); err != nil {
		return nil, err
	}

	if err := file.Validate(); err != nil {
		return nil, err
	}

	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the work could not be recorded").
			WithOp(opNew).
			WithCode(CodeInvalidEbook).
			WithField(field, reason)
	}

	switch {
	case userID == (uuid.UUID{}):
		return nil, invalid("user_id", "a work must name the reader whose collection it is in")
	case device == (uuid.UUID{}):
		return nil, invalid("device_id", "a work must name the device that imported it")
	case at.IsZero():
		return nil, invalid("imported_at", "a work must say when it entered the collection")
	}

	return &Ebook{
		ID: uuid.New(),
		Props: Props{
			UserID:     userID,
			Details:    *details,
			File:       *file,
			ImportedAt: at.UTC().Truncate(crdt.Resolution),
			Revision:   crdt.FirstRevision(device, at),
		},
	}, nil
}

// Restore rebuilds a work already stored, without minting an identifier: the
// id is the one every annotation and every reading position references, and a
// repository that replaced it would orphan all of them.
func Restore(id uuid.UUID, props *Props) *Ebook {
	return &Ebook{ID: id, Props: *props}
}

// BelongsTo reports whether the work is in userID's collection.
//
// Every call that names one has to make this check. A work that belongs to
// somebody else is answered exactly as one that does not exist, or the reply
// would tell a reader which identifiers are somebody else's.
func (e *Ebook) BelongsTo(userID uuid.UUID) bool { return e.UserID == userID }

// Edit replaces the description and stamps the write (RF05).
//
// It takes the whole description rather than the fields that changed, and the
// caller is the one that applied the field mask to build it. That is
// deliberate: the row carries one revision, not one per column, so a write
// that claims two fields is one version of the record — and an entity that
// accepted partial descriptions would have to decide what an omitted field
// means, which is a decision the mask already made.
func (e *Ebook) Edit(details *Details, device uuid.UUID, at time.Time) error {
	if err := details.Validate(); err != nil {
		return err
	}

	if device == (uuid.UUID{}) {
		return errs.New(errs.KindInvalidArgument, "the work could not be edited").
			WithOp(opEdit).
			WithCode(CodeInvalidEbook).
			WithField("device_id", "an edit must name the device that made it")
	}

	e.Details = *details
	e.Revision = e.Revision.Next(device, at)

	return nil
}

// Delete tombstones the work.
//
// The row stays and the tombstone travels, because a row removed outright is
// resurrected by the next node that had not yet heard about the deletion. The
// memberships that filed it under a shelf are tombstoned by the caller in the
// same transaction — this method only says that the work is gone.
func (e *Ebook) Delete(device uuid.UUID, at time.Time) {
	e.Revision = e.Revision.Tombstone(device, at)
}

// IsDeleted reports whether the work has been tombstoned.
func (e *Ebook) IsDeleted() bool { return e.Revision.Deleted }
