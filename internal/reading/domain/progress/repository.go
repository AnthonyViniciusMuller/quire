package progress

import (
	"context"
	"uuid"
)

// The stable machine-readable codes a repository reports.
const (
	// CodeNotFound is no such position, which is what a work this device has
	// never opened looks like. It is not an error the reader caused: the
	// caller answers it by recording a first position rather than by failing.
	CodeNotFound = "reading_progress_not_found"

	// CodeAlreadyExists is a pair the table already holds. It is not an error
	// the reader caused either — two calls from the same device crossing on a
	// flaky network is the ordinary way it happens — so the caller answers it
	// by reading the row and moving it.
	CodeAlreadyExists = "reading_progress_already_exists"
)

// Repository is the port through which the use cases of the reading slice read
// and write where each device stopped. It belongs to the domain; what
// satisfies it lives in internal/reading/infra/repository/progress.
//
// There is no Update that takes a device, and no statement that writes a row
// the caller did not first read. The pair is the natural key and the entity is
// the only thing that may stamp a version, so every write here is the result
// of reading the row and asking the entity to move.
type Repository interface {
	// Create records a first position, and reports errs.KindAlreadyExists when
	// the pair is already written — which the caller answers by reading the
	// row and moving it rather than by failing.
	Create(ctx context.Context, position *Progress) error

	// Update writes back the position and the version the entity stamped.
	Update(ctx context.Context, position *Progress) error

	// GetByPair reads where one device stopped in one work, and reports
	// errs.KindNotFound when that device has never opened it.
	GetByPair(ctx context.Context, ebookID, deviceID uuid.UUID) (*Progress, error)

	// ListForEbook reads every device's position in one work (UC05, RN01).
	//
	// It is not paginated, and the bound is the reader's own: there is one row
	// per device they have ever read the work on, which is the number of
	// appliances a person owns. A page token here would be a cursor over a
	// list of three.
	ListForEbook(ctx context.Context, ebookID uuid.UUID) ([]*Progress, error)
}
