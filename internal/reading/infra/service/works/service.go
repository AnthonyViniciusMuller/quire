// Package works answers whether a reader may write in a work, out of the
// catalogue the library slice owns.
//
// It is the adapter of the Works port the reading use cases hold. Everything in
// this slice hangs off a work — reading.annotations and reading.progress both
// reference library.ebooks and neither references a reader — so establishing
// whose a mark or a position is means reading a row this slice does not own.
//
// It reads it through the library's own repository rather than through a query
// of its own, which is what keeps library.ebooks behind one port in both
// slices: the pagination, the tombstone rule and the ownership check all stay
// in the package that has them, and this adds nothing but the question. That is
// the same shape internal/identity/infra/service/localserver has over the
// federation slice's catalogue, and it is wired the same way — in cmd/quired,
// where the two containers meet, so that neither slice imports the other's di.
package works

import (
	"context"
	"uuid"

	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/reading/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opVisible is the operation reported by this file, in the form the errs
// package expects.
const opVisible = "reading/works: visible"

// Service answers the question out of the library's collection.
type Service struct {
	works libraryebook.Repository
}

// Service satisfies the port the use cases hold.
var _ service.Works = (*Service)(nil)

// New returns the adapter over the library's works repository.
func New(works libraryebook.Repository) *Service { return &Service{works: works} }

// Visible reports nil when the work is in the reader's collection and has not
// been tombstoned.
//
// The three refusals are one answer, and the code is the library's own: a
// client that asked about a work is told there is no such work, in the same
// words the library service would use, because a second spelling of the same
// refusal is something a client could tell the two services apart by.
func (s *Service) Visible(ctx context.Context, ebookID, userID uuid.UUID) error {
	work, err := s.works.GetByID(ctx, ebookID)
	if err != nil {
		return err
	}

	if !work.BelongsTo(userID) || work.IsDeleted() {
		return errs.New(errs.KindNotFound, "no such work in the collection").
			WithOp(opVisible).
			WithCode(libraryebook.CodeNotFound)
	}

	return nil
}
