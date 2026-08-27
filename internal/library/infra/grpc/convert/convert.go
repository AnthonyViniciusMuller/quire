// Package convert translates between the messages of the library contract and
// the vocabulary of the use cases.
//
// It is one package rather than a method on each entity, because the direction
// is the point: the domain must not know that a protobuf exists. Everything
// here reads a value on one side and writes one on the other, and nothing here
// decides anything — a controller that needed a decision would be a use case
// written in the wrong place.
//
// It translates both ways, unlike the federation slice's, and the reason is
// the two enumerations. A format and a kind are named on the wire and stored
// as text, and the mapping between the two spellings has to exist exactly once
// or a work imported as EBOOK_FORMAT_EPUB comes back as something else.
package convert

import (
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// Format renders a stored format as the enumerator the contract names it by.
//
// A value this node does not know becomes UNSPECIFIED rather than an error.
// The contract says why: adding a format is not a breaking change, so a work
// replicated from a node running a later version keeps its number and can
// still be stored, returned and read — the only thing this node cannot do is
// name the format to its own reader.
func Format(format ebook.Format) quirev1.EbookFormat {
	switch format {
	case ebook.FormatEPUB:
		return quirev1.EbookFormat_EBOOK_FORMAT_EPUB
	case ebook.FormatPDF:
		return quirev1.EbookFormat_EBOOK_FORMAT_PDF
	case ebook.FormatMOBI:
		return quirev1.EbookFormat_EBOOK_FORMAT_MOBI
	case ebook.FormatDJVU:
		return quirev1.EbookFormat_EBOOK_FORMAT_DJVU
	case ebook.FormatCBZ:
		return quirev1.EbookFormat_EBOOK_FORMAT_CBZ
	default:
		return quirev1.EbookFormat_EBOOK_FORMAT_UNSPECIFIED
	}
}

// FormatValue reads an enumerator back into what is stored.
//
// UNSPECIFIED becomes the empty string rather than a default, so that a client
// which left the field out is refused by the value object instead of having a
// format chosen for it.
func FormatValue(format quirev1.EbookFormat) string {
	switch format {
	case quirev1.EbookFormat_EBOOK_FORMAT_EPUB:
		return ebook.FormatEPUB.String()
	case quirev1.EbookFormat_EBOOK_FORMAT_PDF:
		return ebook.FormatPDF.String()
	case quirev1.EbookFormat_EBOOK_FORMAT_MOBI:
		return ebook.FormatMOBI.String()
	case quirev1.EbookFormat_EBOOK_FORMAT_DJVU:
		return ebook.FormatDJVU.String()
	case quirev1.EbookFormat_EBOOK_FORMAT_CBZ:
		return ebook.FormatCBZ.String()
	case quirev1.EbookFormat_EBOOK_FORMAT_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// Kind renders what a grouping means as the enumerator the contract names it
// by.
func Kind(kind collection.Kind) quirev1.CollectionKind {
	switch kind {
	case collection.KindCollection:
		return quirev1.CollectionKind_COLLECTION_KIND_COLLECTION
	case collection.KindCategory:
		return quirev1.CollectionKind_COLLECTION_KIND_CATEGORY
	default:
		return quirev1.CollectionKind_COLLECTION_KIND_UNSPECIFIED
	}
}

// KindValue reads an enumerator back into what is stored. UNSPECIFIED becomes
// the empty string, which the value object reads as a shelf — the default the
// column carries.
func KindValue(kind quirev1.CollectionKind) string {
	switch kind {
	case quirev1.CollectionKind_COLLECTION_KIND_COLLECTION:
		return collection.KindCollection.String()
	case quirev1.CollectionKind_COLLECTION_KIND_CATEGORY:
		return collection.KindCategory.String()
	case quirev1.CollectionKind_COLLECTION_KIND_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// Revision renders the replication metadata of a row.
//
// The timestamp is a HybridTimestamp and not a google.protobuf.Timestamp, and
// the contract is emphatic about why: the value is not a wall clock, and a
// distinct message is what stops anything from comparing it against one by
// accident or rendering it to a reader as the time of day (C01).
func Revision(revision crdt.Revision) *quirev1.Revision {
	rendered := &quirev1.Revision{
		VectorClock: &quirev1.VectorClock{Entries: map[string]uint64{}},
		UpdatedAt:   &quirev1.HybridTimestamp{UnixMicros: revision.UpdatedAt.UnixMicro()},
		DeviceId:    revision.DeviceID.String(),
		Deleted:     revision.Deleted,
	}

	// Zero entries are dropped, as the contract requires: a device absent and
	// a device mapped to zero are the same causal history, and sending both
	// forms would make them compare unequal.
	for device, counter := range revision.VectorClock.Compact() {
		rendered.VectorClock.Entries[string(device)] = counter
	}

	return rendered
}

// Ebook renders one work.
func Ebook(work *ebook.Ebook) *quirev1.Ebook {
	rendered := &quirev1.Ebook{
		Id:          work.ID.String(),
		Title:       work.Title.String(),
		Format:      Format(work.Format),
		ContentHash: work.Hash.String(),
		ImportedAt:  timestamppb.New(work.ImportedAt),
		Revision:    Revision(work.Revision),
	}

	// The optional fields are rendered as absent rather than as empty strings.
	// A work whose author is unknown and one whose author is the empty string
	// are different claims, and the wire has a way to say so.
	if !work.Author.IsZero() {
		author := work.Author.String()
		rendered.Author = &author
	}

	if !work.Publisher.IsZero() {
		publisher := work.Publisher.String()
		rendered.Publisher = &publisher
	}

	if !work.Language.IsZero() {
		language := work.Language.String()
		rendered.Language = &language
	}

	if !work.Size.IsZero() {
		size := work.Size.Int64()
		rendered.SizeBytes = &size
	}

	// Metadata that cannot be rendered is left out rather than reported. It
	// was stored as a JSON object, so the only values that can fail here are
	// ones a later version of this node put there, and a reply missing a field
	// a reader never asked about is better than a reply that fails.
	if !work.Extra.IsZero() {
		if extra, err := structpb.NewStruct(work.Extra); err == nil {
			rendered.ExtraMetadata = extra
		}
	}

	return rendered
}

// Ebooks renders a page of works.
func Ebooks(works []*ebook.Ebook) []*quirev1.Ebook {
	rendered := make([]*quirev1.Ebook, 0, len(works))
	for _, work := range works {
		rendered = append(rendered, Ebook(work))
	}

	return rendered
}

// Collection renders one grouping.
func Collection(grouping *collection.Collection) *quirev1.Collection {
	rendered := &quirev1.Collection{
		Id:        grouping.ID.String(),
		Name:      grouping.Name.String(),
		Kind:      Kind(grouping.Kind),
		CreatedAt: timestamppb.New(grouping.CreatedAt),
		Revision:  Revision(grouping.Revision),
	}

	if !grouping.Description.IsZero() {
		description := grouping.Description.String()
		rendered.Description = &description
	}

	return rendered
}

// Collections renders a reader's groupings.
func Collections(groupings []*collection.Collection) []*quirev1.Collection {
	rendered := make([]*quirev1.Collection, 0, len(groupings))
	for _, grouping := range groupings {
		rendered = append(rendered, Collection(grouping))
	}

	return rendered
}

// Content renders what the bytes of a work are, which is the message that
// opens and closes a transfer in either direction.
func Content(record *content.Content) *quirev1.EbookContent {
	return &quirev1.EbookContent{
		ContentHash: record.Hash.String(),
		SizeBytes:   record.Size,
		MediaType:   record.MediaType.String(),
	}
}
