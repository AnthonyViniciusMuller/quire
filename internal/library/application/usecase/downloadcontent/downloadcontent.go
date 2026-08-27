// Package downloadcontent streams the bytes of a work back to the reader
// (RF04).
//
// A node that replicates a reader without their files answers that it does not
// hold them, and that is not an error in the sense of something being wrong:
// holding the metadata and not the file is a state the authorization makes
// legitimate (D02). What the reply says is that the bytes are elsewhere, which
// is what lets a client go and ask the node that has them.
package downloadcontent

import (
	"context"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	command "github.com/anthonyvsmuller/quire/internal/library/application/usecase"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// opExecute is the operation reported by this file, in the form the errs
// package expects.
const opExecute = "library/downloadcontent: execute"

// DownloadContent opens files.
type DownloadContent struct {
	works    ebook.Repository
	contents content.Repository
	blobs    service.BlobStore
}

// DownloadContent satisfies the shape every use case of the slice has.
var _ command.Usecase[Input, Output] = (*DownloadContent)(nil)

// New returns the use case over its dependencies.
func New(works ebook.Repository, contents content.Repository, blobs service.BlobStore) *DownloadContent {
	return &DownloadContent{works: works, contents: contents, blobs: blobs}
}

// Execute opens the bytes of the work, if this node has them.
//
// The work is read first and the file second, and the order is the
// authorization: the digest identifies bytes that may be shared with any
// number of other readers, so a call that opened the object by digest would
// serve any reader who could name one. What decides is the work, which names a
// reader.
//
// Nothing is read from the object store until the reader has been checked, and
// the reply carries what the row says the bytes are rather than what the store
// reports. They are the same thing, and the row is the one this node wrote.
func (d *DownloadContent) Execute(ctx context.Context, input Input) (Output, error) {
	work, err := d.works.GetByID(ctx, input.EbookID)
	if err != nil {
		return Output{}, err
	}

	if !work.BelongsTo(input.UserID) || work.IsDeleted() {
		return Output{}, getebook.NotFound(opExecute)
	}

	record, err := d.contents.GetByHash(ctx, work.Hash)
	if err != nil {
		return Output{}, err
	}

	body, err := d.blobs.Open(ctx, record.Locator)
	if err != nil {
		return Output{}, err
	}

	return Output{Content: record, Body: body}, nil
}
