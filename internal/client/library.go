package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// uploadChunkSize is how much of a file travels in one message.
//
// gRPC refuses a message over four megabytes by default, and a chunk is not the
// only thing in one, so the size is a fraction of that rather than a fraction of
// the file: a client that sized its chunks by the file would work on every book
// it was tested with and fail on the atlas.
const uploadChunkSize = 256 * 1024

// EbookInput is a work being registered (UC01, UC02).
//
// The digest and the length describe the file, and the file itself travels
// separately: the same work imported by two readers converges on one stored
// object, so the bytes are addressed by what they are rather than by whose they
// are. [Fingerprint] is what fills these in from a file on disk.
type EbookInput struct {
	Title     string
	Author    string
	Publisher string
	Language  string

	// Format is the container, in the contract's spelling: epub, pdf, mobi,
	// djvu or cbz.
	Format string

	ContentHash string
	Size        int64

	// Extra is the metadata a format carries and the contract does not name —
	// series, ISBN, subjects (RF05).
	Extra map[string]any
}

// EbookChanges is what an update to a work claims. A nil pointer is a field the
// change does not name, which on a per-field last-writer-wins record is not the
// same as a field it clears: what it does not claim stays with whichever device
// wrote it last.
type EbookChanges struct {
	Title     *string
	Author    *string
	Publisher *string
	Language  *string

	// Extra is claimed when it is not nil. An empty map clears the metadata.
	Extra map[string]any
}

// CollectionInput is a grouping being created (UC03).
type CollectionInput struct {
	Name string

	// Kind is collection or category. The two are the same structure with a
	// different meaning, which is what lets RF05 offer both without a second
	// entity.
	Kind string

	Description string
}

// CollectionChanges is what an update to a grouping claims.
type CollectionChanges struct {
	Name        *string
	Kind        *string
	Description *string
}

// CreateEbook registers the metadata of a work.
//
// Offline it is an insert authored by this device, with the identifier minted
// here; connected it is the RPC, and the node stamps the revision. The reply
// says whether the node already holds the bytes, which it does far more often
// than a reader expects — the same file imported on a second device, or by a
// second reader on this node, is already there.
func (c *Client) CreateEbook(ctx context.Context, in *EbookInput) (Written, error) {
	if c.options.Offline {
		return c.createEbookOffline(in)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.library.CreateEbook(authorized, &quirev1.CreateEbookRequest{
		Ebook: &quirev1.Ebook{
			Title:         in.Title,
			Author:        optional(in.Author),
			Publisher:     optional(in.Publisher),
			Language:      optional(in.Language),
			Format:        ebookFormat(in.Format),
			ContentHash:   in.ContentHash,
			SizeBytes:     optional(in.Size),
			ExtraMetadata: structure(in.Extra),
		},
	})
	if err != nil {
		return Written{}, err
	}

	work := response.GetEbook()
	id := parseID(work.GetId())
	c.rememberRevision(recordKey(entityEbook, id), id, work.GetRevision())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: id, ContentMissing: response.GetContentMissing()}, nil
}

// createEbookOffline authors the same change into the local log.
//
// The format, the digest and the length are required by the node on an insert,
// and they are claimed here even when they are empty so that the refusal names
// the field rather than the row.
func (c *Client) createEbookOffline(in *EbookInput) (Written, error) {
	id := uuid.New()

	changed := delta{}

	for _, claim := range []func() error{
		func() error { return changed.set(fieldTitle, in.Title) },
		func() error { return changed.setText(fieldAuthor, in.Author) },
		func() error { return changed.setText(fieldPublisher, in.Publisher) },
		func() error { return changed.setText(fieldLanguage, in.Language) },
		func() error { return changed.set(fieldFormat, strings.ToLower(in.Format)) },
		func() error { return changed.set(fieldContentHash, in.ContentHash) },
		func() error { return changed.set(fieldSizeBytes, in.Size) },
	} {
		if err := claim(); err != nil {
			return Written{}, err
		}
	}

	if in.Extra != nil {
		if err := changed.set(fieldExtra, in.Extra); err != nil {
			return Written{}, err
		}
	}

	return c.author(entityEbook, recordKey(entityEbook, id), id, kindInsert, changed)
}

// GetEbook returns one work, and refreshes what this device knows of its
// version.
//
// That refresh is the reason a read matters to a client which keeps no
// collection. A change this device makes is answered with the revision it
// produced, so the connected path keeps this device current about its own
// writes; a change another device made while connected is answered to nobody —
// C21 in docs/tcc-corrections.md — so it appears in no page this device pulls,
// and reading the record is how this device learns the version its next change
// has to be stamped on top of.
func (c *Client) GetEbook(ctx context.Context, work uuid.UUID) (*quirev1.Ebook, error) {
	authorized, err := c.call(ctx, "get a work")
	if err != nil {
		return nil, err
	}

	response, err := c.library.GetEbook(authorized, &quirev1.GetEbookRequest{EbookId: work.String()})
	if err != nil {
		return nil, err
	}

	c.rememberEbook(response.GetEbook())

	if err := c.save(); err != nil {
		return nil, err
	}

	return response.GetEbook(), nil
}

// ListEbooks returns a page of the collection, optionally narrowed to one
// grouping, and the token for the page after it.
func (c *Client) ListEbooks(
	ctx context.Context, collection *uuid.UUID, pageSize int32, pageToken string,
) ([]*quirev1.Ebook, string, error) {
	authorized, err := c.call(ctx, "list the collection")
	if err != nil {
		return nil, "", err
	}

	request := &quirev1.ListEbooksRequest{PageSize: pageSize, PageToken: pageToken}
	if collection != nil {
		request.CollectionId = proto(collection.String())
	}

	response, err := c.library.ListEbooks(authorized, request)
	if err != nil {
		return nil, "", err
	}

	for _, work := range response.GetEbooks() {
		c.rememberEbook(work)
	}

	if err := c.save(); err != nil {
		return nil, "", err
	}

	return response.GetEbooks(), response.GetNextPageToken(), nil
}

// rememberEbook records the version of a work this device has just seen.
func (c *Client) rememberEbook(work *quirev1.Ebook) {
	id := parseID(work.GetId())
	c.rememberRevision(recordKey(entityEbook, id), id, work.GetRevision())
}

// UpdateEbook writes the fields the change claims. UC01 is «CRD» because the
// file is not editable; its metadata is, which is RF05.
func (c *Client) UpdateEbook(ctx context.Context, work uuid.UUID, changes EbookChanges) (Written, error) {
	claimed, mask, err := ebookClaims(changes)
	if err != nil {
		return Written{}, err
	}

	if len(mask) == 0 {
		return Written{}, errs.New(errs.KindInvalidArgument, "the change names no field").
			WithOp(opClient).
			WithField("update_mask", "an update must say which fields it writes")
	}

	if c.options.Offline {
		return c.author(entityEbook, recordKey(entityEbook, work), work, kindUpdate, claimed)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.library.UpdateEbook(authorized, &quirev1.UpdateEbookRequest{
		EbookId:    work.String(),
		Ebook:      ebookOf(changes),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: mask},
	})
	if err != nil {
		return Written{}, err
	}

	c.rememberEbook(response.GetEbook())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: work}, nil
}

// ebookClaims renders the change as a delta and as the mask that claims the
// same fields.
//
// The two are built together on purpose: the mask is what the connected path
// carries and the delta is what the disconnected one carries, they name the
// same set, and building them apart is how the two paths would come to differ.
func ebookClaims(changes EbookChanges) (delta, []string, error) {
	claimed := delta{}
	paths := make([]string, 0, 5)

	for field, value := range map[string]*string{
		fieldTitle:     changes.Title,
		fieldAuthor:    changes.Author,
		fieldPublisher: changes.Publisher,
		fieldLanguage:  changes.Language,
	} {
		if value == nil {
			continue
		}

		if err := claimed.setText(field, *value); err != nil {
			return nil, nil, err
		}

		paths = append(paths, field)
	}

	if changes.Extra != nil {
		if err := claimed.set(fieldExtra, changes.Extra); err != nil {
			return nil, nil, err
		}

		paths = append(paths, fieldExtra)
	}

	return claimed, paths, nil
}

// ebookOf renders the change as the message the connected path carries.
func ebookOf(changes EbookChanges) *quirev1.Ebook {
	work := &quirev1.Ebook{}

	if changes.Title != nil {
		work.Title = *changes.Title
	}

	work.Author = changes.Author
	work.Publisher = changes.Publisher
	work.Language = changes.Language

	if changes.Extra != nil {
		work.ExtraMetadata = structure(changes.Extra)
	}

	return work
}

// DeleteEbook tombstones a work. It is never a removal: a row deleted outright
// is resurrected by the next node that had not yet heard about the deletion.
func (c *Client) DeleteEbook(ctx context.Context, work uuid.UUID) (Written, error) {
	if c.options.Offline {
		return c.author(entityEbook, recordKey(entityEbook, work), work, kindDelete, delta{})
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	if _, err = c.library.DeleteEbook(authorized,
		&quirev1.DeleteEbookRequest{EbookId: work.String()}); err != nil {
		return Written{}, err
	}

	return Written{Target: work}, nil
}

// Fingerprint is what a file on disk says about itself: the digest that names
// it across the whole federation, its length, what it is, and the format the
// contract calls that.
type Fingerprint struct {
	ContentHash string
	Size        int64
	MediaType   string
	Format      string
}

// Digest reads a file and describes it.
//
// The whole file is read, because the digest is what the object will be stored
// under and a name that promised bytes nobody hashed would be a name that
// cannot be checked. It is the same digest the node computes as the upload
// arrives, and the upload is refused if the two disagree.
func Digest(path string) (Fingerprint, error) {
	file, err := os.Open(path) //nolint:gosec // the caller names their own file
	if err != nil {
		return Fingerprint{}, errs.Wrap(err, errs.KindNotFound, "the file could not be read").
			WithOp(opClient).
			WithField("file", "there is nothing to import at that path")
	}

	defer func() { _ = file.Close() }()

	digest := sha256.New()

	size, err := io.Copy(digest, file)
	if err != nil {
		return Fingerprint{}, errs.Wrap(err, errs.KindInternal, "the file could not be read").
			WithOp(opClient)
	}

	format, mediaType := describe(path)

	return Fingerprint{
		ContentHash: hex.EncodeToString(digest.Sum(nil)),
		Size:        size,
		MediaType:   mediaType,
		Format:      format,
	}, nil
}

// describe names the format and the media type of a file by its extension.
//
// It is a guess, and it is the client's guess to make: the node records what it
// was told the bytes are and never opens them. An extension it does not know
// leaves both empty, so that the reader is asked rather than having a format
// invented for them.
func describe(path string) (format, mediaType string) {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "epub":
		return "epub", "application/epub+zip"
	case "pdf":
		return "pdf", "application/pdf"
	case "mobi":
		return "mobi", "application/x-mobipocket-ebook"
	case "djvu":
		return "djvu", "image/vnd.djvu"
	case "cbz":
		return "cbz", "application/vnd.comicbook+zip"
	default:
		return "", ""
	}
}

// UploadContent streams the bytes of a work this node does not hold yet (UC02).
//
// The description travels first and the bytes after it, which is what lets the
// node refuse an oversized or unsupported file before any of it is sent. The
// call carries no work identifier, as C16 records: what it stores is bytes
// under their digest, and which works point at them is the metadata's business.
func (c *Client) UploadContent(ctx context.Context, path string) (*quirev1.EbookContent, error) {
	authorized, err := c.call(ctx, "upload a file")
	if err != nil {
		return nil, err
	}

	fingerprint, err := Digest(path)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path) //nolint:gosec // the caller names their own file
	if err != nil {
		return nil, errs.Wrap(err, errs.KindNotFound, "the file could not be read").
			WithOp(opClient).
			WithField("file", "there is nothing to import at that path")
	}

	defer func() { _ = file.Close() }()

	stream, err := c.library.UploadEbookContent(authorized)
	if err != nil {
		return nil, err
	}

	if err = stream.Send(&quirev1.UploadEbookContentRequest{
		Payload: &quirev1.UploadEbookContentRequest_Content{
			Content: &quirev1.EbookContent{
				ContentHash: fingerprint.ContentHash,
				SizeBytes:   fingerprint.Size,
				MediaType:   fingerprint.MediaType,
			},
		},
	}); err != nil {
		return nil, sendFailure(err)
	}

	if failure := sendChunks(stream, file); failure != nil {
		return nil, failure
	}

	response, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}

	return response.GetContent(), nil
}

// sendChunks writes the file into the stream.
//
// A send that fails is reported as what it is rather than as the failure of the
// call: the node closes the stream when it refuses the upload, and the error the
// client then sees on the send says nothing about why. The status is on the
// receive, which is why this one only says the transfer stopped.
func sendChunks(stream quirev1.LibraryService_UploadEbookContentClient, file io.Reader) error {
	buffer := make([]byte, uploadChunkSize)

	for {
		read, err := file.Read(buffer)
		if read > 0 {
			if failure := stream.Send(&quirev1.UploadEbookContentRequest{
				Payload: &quirev1.UploadEbookContentRequest_Chunk{Chunk: buffer[:read]},
			}); failure != nil {
				return sendFailure(failure)
			}
		}

		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return errs.Wrap(err, errs.KindInternal, "the file could not be read").WithOp(opClient)
		}
	}
}

// sendFailure reports a stream the node stopped receiving on. io.EOF here means
// the node has closed it and the reason is on the receive, so it is not
// translated into a transport failure.
func sendFailure(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}

	return errs.Wrap(err, errs.KindUnavailable, "the transfer stopped").WithOp(opClient)
}

// DownloadContent streams the bytes of a work into out, and returns what the
// node said they are.
//
// A node that replicates this reader without their files answers that it does
// not hold them, which is a state the authorization makes legitimate rather
// than an error (D02).
func (c *Client) DownloadContent(
	ctx context.Context, work uuid.UUID, out io.Writer,
) (*quirev1.EbookContent, error) {
	authorized, err := c.call(ctx, "download a file")
	if err != nil {
		return nil, err
	}

	stream, err := c.library.DownloadEbookContent(authorized,
		&quirev1.DownloadEbookContentRequest{EbookId: work.String()})
	if err != nil {
		return nil, err
	}

	var content *quirev1.EbookContent

	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return content, nil
		}

		if err != nil {
			return nil, err
		}

		if described := message.GetContent(); described != nil {
			content = described

			continue
		}

		if _, err = out.Write(message.GetChunk()); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "the file could not be written").
				WithOp(opClient)
		}
	}
}

// CreateCollection creates a grouping over the collection (UC03).
func (c *Client) CreateCollection(ctx context.Context, in *CollectionInput) (Written, error) {
	if c.options.Offline {
		return c.createCollectionOffline(in)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.library.CreateCollection(authorized, &quirev1.CreateCollectionRequest{
		Collection: &quirev1.Collection{
			Name:        in.Name,
			Kind:        collectionKind(in.Kind),
			Description: optional(in.Description),
		},
	})
	if err != nil {
		return Written{}, err
	}

	grouping := response.GetCollection()
	id := parseID(grouping.GetId())
	c.rememberRevision(recordKey(entityCollection, id), id, grouping.GetRevision())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: id}, nil
}

// createCollectionOffline authors the same change into the local log.
func (c *Client) createCollectionOffline(in *CollectionInput) (Written, error) {
	id := uuid.New()

	changed := delta{}

	for _, claim := range []func() error{
		func() error { return changed.set(fieldName, in.Name) },
		func() error { return changed.set(fieldKind, strings.ToLower(in.Kind)) },
		func() error { return changed.setText(fieldDescription, in.Description) },
	} {
		if err := claim(); err != nil {
			return Written{}, err
		}
	}

	return c.author(entityCollection, recordKey(entityCollection, id), id, kindInsert, changed)
}

// GetCollection returns one grouping.
func (c *Client) GetCollection(ctx context.Context, grouping uuid.UUID) (*quirev1.Collection, error) {
	authorized, err := c.call(ctx, "get a grouping")
	if err != nil {
		return nil, err
	}

	response, err := c.library.GetCollection(authorized,
		&quirev1.GetCollectionRequest{CollectionId: grouping.String()})
	if err != nil {
		return nil, err
	}

	c.rememberCollection(response.GetCollection())

	if err := c.save(); err != nil {
		return nil, err
	}

	return response.GetCollection(), nil
}

// ListCollections returns the reader's groupings, optionally narrowed to the
// ones a work is filed under.
func (c *Client) ListCollections(ctx context.Context, work *uuid.UUID) ([]*quirev1.Collection, error) {
	authorized, err := c.call(ctx, "list the groupings")
	if err != nil {
		return nil, err
	}

	request := &quirev1.ListCollectionsRequest{}
	if work != nil {
		request.EbookId = proto(work.String())
	}

	response, err := c.library.ListCollections(authorized, request)
	if err != nil {
		return nil, err
	}

	for _, grouping := range response.GetCollections() {
		c.rememberCollection(grouping)
	}

	if err := c.save(); err != nil {
		return nil, err
	}

	return response.GetCollections(), nil
}

// rememberCollection records the version of a grouping this device has just
// seen.
func (c *Client) rememberCollection(grouping *quirev1.Collection) {
	id := parseID(grouping.GetId())
	c.rememberRevision(recordKey(entityCollection, id), id, grouping.GetRevision())
}

// UpdateCollection writes the fields the change claims.
func (c *Client) UpdateCollection(
	ctx context.Context, grouping uuid.UUID, changes CollectionChanges,
) (Written, error) {
	claimed := delta{}
	paths := make([]string, 0, 3)
	message := &quirev1.Collection{}

	if changes.Name != nil {
		if err := claimed.set(fieldName, *changes.Name); err != nil {
			return Written{}, err
		}

		message.Name = *changes.Name
		paths = append(paths, fieldName)
	}

	if changes.Kind != nil {
		if err := claimed.set(fieldKind, strings.ToLower(*changes.Kind)); err != nil {
			return Written{}, err
		}

		message.Kind = collectionKind(*changes.Kind)
		paths = append(paths, fieldKind)
	}

	if changes.Description != nil {
		if err := claimed.setText(fieldDescription, *changes.Description); err != nil {
			return Written{}, err
		}

		message.Description = changes.Description
		paths = append(paths, fieldDescription)
	}

	if len(paths) == 0 {
		return Written{}, errs.New(errs.KindInvalidArgument, "the change names no field").
			WithOp(opClient).
			WithField("update_mask", "an update must say which fields it writes")
	}

	if c.options.Offline {
		return c.author(entityCollection, recordKey(entityCollection, grouping), grouping, kindUpdate, claimed)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.library.UpdateCollection(authorized, &quirev1.UpdateCollectionRequest{
		CollectionId: grouping.String(),
		Collection:   message,
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: paths},
	})
	if err != nil {
		return Written{}, err
	}

	c.rememberCollection(response.GetCollection())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: grouping}, nil
}

// DeleteCollection tombstones a grouping. The works survive it: deleting a
// shelf is not deleting what was on it, and the filings are tombstoned with it.
func (c *Client) DeleteCollection(ctx context.Context, grouping uuid.UUID) (Written, error) {
	if c.options.Offline {
		return c.author(entityCollection, recordKey(entityCollection, grouping), grouping, kindDelete, delta{})
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	if _, err = c.library.DeleteCollection(authorized,
		&quirev1.DeleteCollectionRequest{CollectionId: grouping.String()}); err != nil {
		return Written{}, err
	}

	return Written{Target: grouping}, nil
}

// AddToCollection files a work under a grouping. Repeating it is not an error:
// the pair is unique (C06) and the filing is a register that is set, not a row
// that is appended.
func (c *Client) AddToCollection(ctx context.Context, work, grouping uuid.UUID) (Written, error) {
	return c.file(ctx, work, grouping, kindInsert)
}

// RemoveFromCollection clears that register, and is idempotent for the same
// reason.
func (c *Client) RemoveFromCollection(ctx context.Context, work, grouping uuid.UUID) (Written, error) {
	return c.file(ctx, work, grouping, kindDelete)
}

// file sets or clears the register both ways round.
//
// Offline the delta carries the pair, always, because that is what identifies
// the filing across the federation: the row has a surrogate key each replica
// mints for itself, so the identifier the operation names is only what this
// device calls it (C18).
func (c *Client) file(ctx context.Context, work, grouping uuid.UUID, kind string) (Written, error) {
	if c.options.Offline {
		key := recordKey(entityFiling, work, grouping)

		changed := delta{}
		if err := changed.set(fieldEbookID, work.String()); err != nil {
			return Written{}, err
		}

		if err := changed.set(fieldCollectionID, grouping.String()); err != nil {
			return Written{}, err
		}

		return c.author(entityFiling, key, c.target(key), kind, changed)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	if kind == kindDelete {
		_, err = c.library.RemoveEbookFromCollection(authorized, &quirev1.RemoveEbookFromCollectionRequest{
			EbookId:      work.String(),
			CollectionId: grouping.String(),
		})
	} else {
		_, err = c.library.AddEbookToCollection(authorized, &quirev1.AddEbookToCollectionRequest{
			EbookId:      work.String(),
			CollectionId: grouping.String(),
		})
	}

	if err != nil {
		return Written{}, err
	}

	return Written{Target: work}, nil
}

// ebookFormat reads a format name into the enumerator the contract names it by.
//
// The names come from the generated enumeration rather than from a list written
// here, so a format added to the contract is a format this client can name
// without being edited.
func ebookFormat(name string) quirev1.EbookFormat {
	value, ok := quirev1.EbookFormat_value["EBOOK_FORMAT_"+strings.ToUpper(strings.TrimSpace(name))]
	if !ok {
		return quirev1.EbookFormat_EBOOK_FORMAT_UNSPECIFIED
	}

	return quirev1.EbookFormat(value)
}

// FormatName renders a format as the reader spells it, which is also how the
// delta of an offline change carries it.
func FormatName(format quirev1.EbookFormat) string {
	return strings.ToLower(strings.TrimPrefix(format.String(), "EBOOK_FORMAT_"))
}

// collectionKind reads a grouping's kind into its enumerator.
func collectionKind(name string) quirev1.CollectionKind {
	value, ok := quirev1.CollectionKind_value["COLLECTION_KIND_"+strings.ToUpper(strings.TrimSpace(name))]
	if !ok {
		return quirev1.CollectionKind_COLLECTION_KIND_UNSPECIFIED
	}

	return quirev1.CollectionKind(value)
}

// CollectionKindName renders a grouping's kind as the reader spells it.
func CollectionKindName(kind quirev1.CollectionKind) string {
	return strings.ToLower(strings.TrimPrefix(kind.String(), "COLLECTION_KIND_"))
}

// optional returns a pointer to value, or nil for the zero value, which is how
// this contract spells a field the caller did not give.
func optional[T comparable](value T) *T {
	var zero T

	if value == zero {
		return nil
	}

	return &value
}

// structure renders free-form metadata as the message the contract carries it
// in. A map it cannot render is rendered as empty rather than failing the call:
// the node validates what it receives, and a client that refused here would be
// refusing on behalf of a rule it does not own.
func structure(fields map[string]any) *structpb.Struct {
	if fields == nil {
		return nil
	}

	rendered, err := structpb.NewStruct(fields)
	if err != nil {
		return &structpb.Struct{}
	}

	return rendered
}
