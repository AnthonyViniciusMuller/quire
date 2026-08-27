// Package createebook is the create half of UC01: it records the metadata of a
// work and says whether this node already holds the file (RF01, RF04).
//
// The bytes travel separately, which is what makes this two calls rather than
// one. A reader importing a work their own second device already uploaded, or
// one another reader on this node happens to have, should pay for the metadata
// and not for the transfer — and the only way to know is to have written the
// row and asked.
package createebook

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// CreateEbook records works.
type CreateEbook struct {
	works    ebook.Repository
	contents content.Repository
	clock    service.Clock
}

// CreateEbook satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*CreateEbook)(nil)

// New returns the use case over its dependencies.
func New(works ebook.Repository, contents content.Repository, clock service.Clock) *CreateEbook {
	return &CreateEbook{works: works, contents: contents, clock: clock}
}

// Execute writes the row and reports whether the bytes are here.
//
// The two are deliberately not one transaction. Whether this node holds a file
// is a fact about its disk that any other call may change a moment later — a
// second reader finishing an upload of the same digest — so reading it inside
// the write would buy an isolation the answer does not have. What the client
// receives is true when it was read, and an upload of a file that has since
// arrived costs a transfer and is otherwise correct.
//
//nolint:gocritic // hugeParam: the Usecase interface fixes this signature by value.
func (c *CreateEbook) Execute(ctx context.Context, input Input) (Output, error) {
	details, err := parseDetails(&input)
	if err != nil {
		return Output{}, err
	}

	file, err := parseFile(&input)
	if err != nil {
		return Output{}, err
	}

	work, err := ebook.New(input.UserID, details, file, input.DeviceID, c.clock.Now())
	if err != nil {
		return Output{}, err
	}

	if err = c.works.Create(ctx, work); err != nil {
		return Output{}, err
	}

	held, err := c.contents.Has(ctx, work.Hash)
	if err != nil {
		return Output{}, err
	}

	return Output{Ebook: work, ContentMissing: !held}, nil
}

// parseDetails turns what the client sent into the description the entity
// takes, refusing the first field that cannot be one.
func parseDetails(input *Input) (*ebook.Details, error) {
	title, err := ebook.ParseTitle(input.Title)
	if err != nil {
		return nil, err
	}

	author, err := ebook.ParseAuthor(input.Author)
	if err != nil {
		return nil, err
	}

	publisher, err := ebook.ParsePublisher(input.Publisher)
	if err != nil {
		return nil, err
	}

	language, err := ebook.ParseLanguage(input.Language)
	if err != nil {
		return nil, err
	}

	return &ebook.Details{
		Title:     title,
		Author:    author,
		Publisher: publisher,
		Language:  language,
		Extra:     ebook.Metadata(input.Extra),
	}, nil
}

// parseFile turns what the client sent into the file the entity takes.
func parseFile(input *Input) (*ebook.File, error) {
	format, err := ebook.ParseFormat(input.Format)
	if err != nil {
		return nil, err
	}

	hash, err := ebook.ParseContentHash(input.ContentHash)
	if err != nil {
		return nil, err
	}

	size := ebook.Size(input.Size)
	if err := size.Validate(); err != nil {
		return nil, err
	}

	return &ebook.File{Format: format, Hash: hash, Size: size}, nil
}
