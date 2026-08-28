package client

import (
	"context"
	"strings"
	"uuid"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// AnnotationInput is a mark the reader left in a work (UC04).
type AnnotationInput struct {
	// Ebook is the work the mark is in. A mark is made in a work and stays in
	// it: nothing here can move one.
	Ebook uuid.UUID

	// Kind is note, highlight or bookmark.
	Kind string

	// Text is required for a note, which is otherwise an empty note, and
	// optional on the other two.
	Text string

	// Locator is the passage, expressed so that it survives the format: a CFI
	// in an EPUB, a page in a PDF.
	Locator string
}

// AnnotationChanges is what an update to a mark claims.
type AnnotationChanges struct {
	Kind    *string
	Text    *string
	Locator *string
}

// CreateAnnotation writes a mark.
func (c *Client) CreateAnnotation(ctx context.Context, in *AnnotationInput) (Written, error) {
	if c.options.Offline {
		return c.createAnnotationOffline(in)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.reading.CreateAnnotation(authorized, &quirev1.CreateAnnotationRequest{
		Annotation: &quirev1.Annotation{
			EbookId: in.Ebook.String(),
			Kind:    annotationKind(in.Kind),
			Text:    optional(in.Text),
			Locator: in.Locator,
		},
	})
	if err != nil {
		return Written{}, err
	}

	mark := response.GetAnnotation()
	id := parseID(mark.GetId())
	c.rememberRevision(recordKey(entityAnnotation, id), id, mark.GetRevision())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: id}, nil
}

// createAnnotationOffline authors the same change into the local log.
//
// The work is claimed here and nowhere else: reading.annotations references the
// work and not the reader, so it is what establishes whose the mark is, and the
// node refuses an insert that does not name one.
func (c *Client) createAnnotationOffline(in *AnnotationInput) (Written, error) {
	id := uuid.New()

	changed := delta{}

	for _, claim := range []func() error{
		func() error { return changed.set(fieldEbookID, in.Ebook.String()) },
		func() error { return changed.set(fieldKind, strings.ToLower(in.Kind)) },
		func() error { return changed.set(fieldLocator, in.Locator) },
		func() error { return changed.setText(fieldText, in.Text) },
	} {
		if err := claim(); err != nil {
			return Written{}, err
		}
	}

	return c.author(entityAnnotation, recordKey(entityAnnotation, id), id, kindInsert, changed)
}

// GetAnnotation returns one mark, and refreshes what this device knows of its
// version.
func (c *Client) GetAnnotation(ctx context.Context, mark uuid.UUID) (*quirev1.Annotation, error) {
	authorized, err := c.call(ctx, "get a mark")
	if err != nil {
		return nil, err
	}

	response, err := c.reading.GetAnnotation(authorized,
		&quirev1.GetAnnotationRequest{AnnotationId: mark.String()})
	if err != nil {
		return nil, err
	}

	c.rememberAnnotation(response.GetAnnotation())

	if err := c.save(); err != nil {
		return nil, err
	}

	return response.GetAnnotation(), nil
}

// ListAnnotations returns a page of what the reader wrote in one work.
func (c *Client) ListAnnotations(
	ctx context.Context, work uuid.UUID, pageSize int32, pageToken string,
) ([]*quirev1.Annotation, string, error) {
	authorized, err := c.call(ctx, "list the marks")
	if err != nil {
		return nil, "", err
	}

	response, err := c.reading.ListAnnotations(authorized, &quirev1.ListAnnotationsRequest{
		EbookId:   work.String(),
		PageSize:  pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		return nil, "", err
	}

	for _, mark := range response.GetAnnotations() {
		c.rememberAnnotation(mark)
	}

	if err := c.save(); err != nil {
		return nil, "", err
	}

	return response.GetAnnotations(), response.GetNextPageToken(), nil
}

// rememberAnnotation records the version of a mark this device has just seen.
func (c *Client) rememberAnnotation(mark *quirev1.Annotation) {
	id := parseID(mark.GetId())
	c.rememberRevision(recordKey(entityAnnotation, id), id, mark.GetRevision())
}

// UpdateAnnotation writes the fields the change claims.
//
// A mark edited from a second device is why the tie-break records the last
// writer rather than the originator (C10): after this call the two are
// different devices, and the rule needs the one whose write the record
// reflects.
func (c *Client) UpdateAnnotation(
	ctx context.Context, mark uuid.UUID, changes AnnotationChanges,
) (Written, error) {
	claimed := delta{}
	paths := make([]string, 0, 3)
	message := &quirev1.Annotation{}

	if changes.Kind != nil {
		if err := claimed.set(fieldKind, strings.ToLower(*changes.Kind)); err != nil {
			return Written{}, err
		}

		message.Kind = annotationKind(*changes.Kind)
		paths = append(paths, fieldKind)
	}

	if changes.Text != nil {
		if err := claimed.setText(fieldText, *changes.Text); err != nil {
			return Written{}, err
		}

		message.Text = changes.Text
		paths = append(paths, fieldText)
	}

	if changes.Locator != nil {
		if err := claimed.set(fieldLocator, *changes.Locator); err != nil {
			return Written{}, err
		}

		message.Locator = *changes.Locator
		paths = append(paths, fieldLocator)
	}

	if len(paths) == 0 {
		return Written{}, errs.New(errs.KindInvalidArgument, "the change names no field").
			WithOp(opClient).
			WithField("update_mask", "an update must say which fields it writes")
	}

	if c.options.Offline {
		return c.author(entityAnnotation, recordKey(entityAnnotation, mark), mark, kindUpdate, claimed)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.reading.UpdateAnnotation(authorized, &quirev1.UpdateAnnotationRequest{
		AnnotationId: mark.String(),
		Annotation:   message,
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: paths},
	})
	if err != nil {
		return Written{}, err
	}

	c.rememberAnnotation(response.GetAnnotation())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: mark}, nil
}

// DeleteAnnotation tombstones a mark, so that a node which had not yet heard
// about the deletion cannot resurrect it by replying with its own copy.
func (c *Client) DeleteAnnotation(ctx context.Context, mark uuid.UUID) (Written, error) {
	if c.options.Offline {
		return c.author(entityAnnotation, recordKey(entityAnnotation, mark), mark, kindDelete, delta{})
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	if _, err = c.reading.DeleteAnnotation(authorized,
		&quirev1.DeleteAnnotationRequest{AnnotationId: mark.String()}); err != nil {
		return Written{}, err
	}

	return Written{Target: mark}, nil
}

// UpdateProgress records where this device has reached in a work (UC05).
//
// The device is not a parameter on either path. The row has one writer and it
// is the one the row names, which is C05 expressed in the types rather than in
// a check somebody has to remember: a call that could name a device would be a
// call that could move another device's bookmark.
func (c *Client) UpdateProgress(
	ctx context.Context, work uuid.UUID, locator string, percent *float64,
) (Written, error) {
	if c.options.Offline {
		return c.updateProgressOffline(work, locator, percent)
	}

	authorized, err := c.authorized(ctx)
	if err != nil {
		return Written{}, err
	}

	response, err := c.reading.UpdateReadingProgress(authorized, &quirev1.UpdateReadingProgressRequest{
		EbookId: work.String(),
		Locator: locator,
		Percent: percent,
	})
	if err != nil {
		return Written{}, err
	}

	c.rememberProgress(response.GetProgress())

	if err := c.save(); err != nil {
		return Written{}, err
	}

	return Written{Target: work}, nil
}

// updateProgressOffline authors the same change into the local log.
//
// It is an insert the first time this device reports a position in the work and
// an update afterwards, which is what the node's own reconciler decides by
// looking for the pair — the kind is what this device knows, and the pair is
// what settles it there.
func (c *Client) updateProgressOffline(work uuid.UUID, locator string, percent *float64) (Written, error) {
	key := recordKey(entityPosition, work)

	changed := delta{}
	if err := changed.set(fieldEbookID, work.String()); err != nil {
		return Written{}, err
	}

	if err := changed.set(fieldLocator, locator); err != nil {
		return Written{}, err
	}

	if percent != nil {
		if err := changed.set(fieldPercent, *percent); err != nil {
			return Written{}, err
		}
	}

	kind := kindUpdate
	if _, known := c.state.Records[key]; !known {
		kind = kindInsert
	}

	return c.author(entityPosition, key, c.target(key), kind, changed)
}

// ListProgress returns every device's position in one work (RN01).
//
// The node returns them all and picks none, because which one to resume from —
// the furthest, the most recent, or a question to the reader — is the client's
// decision to make.
func (c *Client) ListProgress(ctx context.Context, work uuid.UUID) ([]*quirev1.ReadingProgress, error) {
	authorized, err := c.call(ctx, "list the positions")
	if err != nil {
		return nil, err
	}

	response, err := c.reading.ListReadingProgress(authorized,
		&quirev1.ListReadingProgressRequest{EbookId: work.String()})
	if err != nil {
		return nil, err
	}

	for _, position := range response.GetProgress() {
		c.rememberProgress(position)
	}

	if err := c.save(); err != nil {
		return nil, err
	}

	return response.GetProgress(), nil
}

// rememberProgress records the version of a position this device has just seen.
//
// Only its own. The other rows belong to the reader's other devices, and this
// device can never author a change to one of them — remembering their versions
// would be remembering a clock it has no way to tick.
func (c *Client) rememberProgress(position *quirev1.ReadingProgress) {
	if position == nil || parseID(position.GetDeviceId()) != c.state.Device.ID {
		return
	}

	work := parseID(position.GetEbookId())
	key := recordKey(entityPosition, work)

	c.observe(position.GetUpdatedAt())
	c.remember(key, c.target(key), readClock(position.GetVectorClock()))
}

// annotationKind reads the kind of a mark into its enumerator.
func annotationKind(name string) quirev1.AnnotationKind {
	value, ok := quirev1.AnnotationKind_value["ANNOTATION_KIND_"+strings.ToUpper(strings.TrimSpace(name))]
	if !ok {
		return quirev1.AnnotationKind_ANNOTATION_KIND_UNSPECIFIED
	}

	return quirev1.AnnotationKind(value)
}

// AnnotationKindName renders the kind of a mark as the reader spells it.
func AnnotationKindName(kind quirev1.AnnotationKind) string {
	return strings.ToLower(strings.TrimPrefix(kind.String(), "ANNOTATION_KIND_"))
}
