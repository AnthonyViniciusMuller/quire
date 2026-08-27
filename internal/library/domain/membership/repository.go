package membership

import (
	"context"
	"time"
	"uuid"
)

// The stable machine-readable codes a repository reports.
const (
	// CodeNotFound is no such filing, which is what a pair that has never been
	// written looks like.
	CodeNotFound = "membership_not_found"
	// CodeAlreadyFiled is a pair the table already holds. It is not an error
	// the reader caused: the row is a register, so the caller answers it by
	// reading the row and setting that register rather than by failing.
	CodeAlreadyFiled = "membership_already_exists"
)

// Repository is the port through which the use cases of the library slice read
// and write what is filed where. It belongs to the domain; what satisfies it
// lives in internal/library/infra/repository/membership.
type Repository interface {
	// Create records a filing, and reports errs.KindAlreadyExists when the
	// pair is already written — which the caller answers by reading the row
	// and setting its register rather than by failing.
	Create(ctx context.Context, filing *Membership) error

	// Update writes back the register and the revision the entity stamped.
	Update(ctx context.Context, filing *Membership) error

	// GetByPair reads the filing of one work under one grouping, set or
	// cleared, and reports errs.KindNotFound when the pair has never been
	// written.
	GetByPair(ctx context.Context, ebookID, collectionID uuid.UUID) (*Membership, error)

	// ClearForEbook tombstones every filing of one work, which is what
	// deleting the work does to the shelves it was on.
	//
	// It stamps each row with the device and the instant of the write that
	// caused it, because these are replicated rows like any other: a peer that
	// receives the work's tombstone and not its memberships' would keep
	// showing the work on a shelf it is no longer in.
	ClearForEbook(ctx context.Context, ebookID, device uuid.UUID, at time.Time) error

	// ClearForCollection tombstones every filing under one grouping, which is
	// what deleting the grouping does. The works themselves survive it.
	ClearForCollection(ctx context.Context, collectionID, device uuid.UUID, at time.Time) error
}
