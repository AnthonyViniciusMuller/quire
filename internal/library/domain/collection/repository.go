package collection

import (
	"context"
	"uuid"
)

// CodeNotFound is no such grouping. It is also the answer to a grouping that
// is somebody else's and to one that has been tombstoned, because a reply that
// distinguished them would tell a reader which identifiers exist.
const CodeNotFound = "collection_not_found"

// Repository is the port through which the use cases of the library slice read
// and write a reader's groupings. It belongs to the domain; what satisfies it
// lives in internal/library/infra/repository/collection.
type Repository interface {
	// Create records a grouping.
	Create(ctx context.Context, grouping *Collection) error

	// Update writes back the description, the tombstone and the revision the
	// entity stamped.
	Update(ctx context.Context, grouping *Collection) error

	// GetByID reads a grouping by primary key, tombstoned or not, for the
	// reason a work's read returns tombstones.
	GetByID(ctx context.Context, id uuid.UUID) (*Collection, error)

	// GetByIDForUpdate is GetByID holding the row until the transaction ends.
	//
	// It is what makes deleting a grouping and filing a work under it at the
	// same moment reach one answer. Both read the grouping and then write a
	// membership that references it; the read cannot see a write committed
	// after its own statement began, so the two calls take this lock first and
	// serialize on the row they disagree about.
	//
	// It must be called inside a unit of work. Outside one the lock is
	// released with the statement, which is to say immediately.
	GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*Collection, error)

	// List reads a reader's groupings, ordered by name so that the list does
	// not reshuffle between two calls, and tombstoned ones are never in it.
	//
	// It is not paginated, unlike the works themselves: a reader defines
	// shelves by hand and there are as many of them as they had patience for.
	//
	// When ebookID is not the zero value the reply is narrowed to the
	// groupings that one work is filed under, which is the reverse of the
	// filter a page of works offers.
	List(ctx context.Context, userID, ebookID uuid.UUID) ([]*Collection, error)
}
