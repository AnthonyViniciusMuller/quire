// Package listprogress reads every device's position in one work (UC05, RN01).
//
// Every device's and not the reader's: RN01 says a reader resumes where they
// stopped, and on a reader with three appliances there are three answers to
// that. Which one to show — the furthest position, the most recent one, or a
// prompt asking which to resume from — is the client's decision, and this call
// does not make it by returning a single row.
package listprogress

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	command "github.com/anthonyvsmuller/quire/internal/reading/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
)

// ListProgress reads reading positions.
type ListProgress struct {
	positions progress.Repository
	works     service.Works
}

// ListProgress satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*ListProgress)(nil)

// New returns the use case over its dependencies.
func New(positions progress.Repository, works service.Works) *ListProgress {
	return &ListProgress{positions: positions, works: works}
}

// Execute reads them.
//
// It is not paginated, and the bound is the reader's own: one row per appliance
// they have ever read the work on, which is the number of devices a person
// owns. A page token here would be a cursor over a list of three.
//
// The work is checked first, as it is for a page of marks and for the same
// reason: the rows are scoped by the work, so a work belonging to somebody else
// would return where that reader had stopped in it.
func (l *ListProgress) Execute(ctx context.Context, input Input) (Output, error) {
	if err := l.works.Visible(ctx, input.EbookID, input.UserID); err != nil {
		return Output{}, err
	}

	positions, err := l.positions.ListForEbook(ctx, input.EbookID)
	if err != nil {
		return Output{}, err
	}

	return Output{Progress: positions}, nil
}
