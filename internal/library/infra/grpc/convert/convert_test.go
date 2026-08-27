package convert_test

import (
	"testing"
	"time"
	"uuid"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/convert"
	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
)

// at is when the values below were written, and digest is a well-formed
// content hash.
var at = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// A work imported as one format and returned as another would be a work the
// reader can no longer open, so the mapping has to be exact in both
// directions.
func TestFormatRoundTrips(t *testing.T) {
	t.Parallel()

	for _, format := range []ebook.Format{
		ebook.FormatEPUB, ebook.FormatPDF, ebook.FormatMOBI, ebook.FormatDJVU, ebook.FormatCBZ,
	} {
		if back := convert.FormatValue(convert.Format(format)); back != format.String() {
			t.Errorf("%q came back as %q", format, back)
		}
	}
}

// Adding a format is not a breaking change: a work replicated from a node
// running a later version keeps its number and stays readable, and the only
// thing this node cannot do is name the format to its own reader.
func TestFormatOfALaterVersionIsUnspecifiedRatherThanAnError(t *testing.T) {
	t.Parallel()

	if rendered := convert.Format("azw3"); rendered != quirev1.EbookFormat_EBOOK_FORMAT_UNSPECIFIED {
		t.Errorf("an unknown format rendered as %v", rendered)
	}
}

// A client that left the field out must be refused by the value object rather
// than have a format chosen for it.
func TestFormatValueOfUnspecifiedIsNotADefault(t *testing.T) {
	t.Parallel()

	if value := convert.FormatValue(quirev1.EbookFormat_EBOOK_FORMAT_UNSPECIFIED); value != "" {
		t.Errorf("an unstated format became %q", value)
	}
}

func TestKindRoundTrips(t *testing.T) {
	t.Parallel()

	for _, kind := range []collection.Kind{collection.KindCollection, collection.KindCategory} {
		if back := convert.KindValue(convert.Kind(kind)); back != kind.String() {
			t.Errorf("%q came back as %q", kind, back)
		}
	}

	// An unstated kind is a shelf, which is the default the column carries and
	// what the value object reads an empty string as.
	if value := convert.KindValue(quirev1.CollectionKind_COLLECTION_KIND_UNSPECIFIED); value != "" {
		t.Errorf("an unstated kind became %q", value)
	}
}

func TestEbookRendersTheOptionalFieldsAsAbsent(t *testing.T) {
	t.Parallel()

	work, err := ebook.New(uuid.New(),
		&ebook.Details{Title: "untitled.epub"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest},
		uuid.New(), at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rendered := convert.Ebook(work)

	switch {
	case rendered.Author != nil || rendered.Publisher != nil || rendered.Language != nil:
		t.Error("a field the file said nothing about was rendered as something it claimed")
	case rendered.SizeBytes != nil:
		t.Error("an absent length was rendered as a claim about the file")
	case rendered.ExtraMetadata != nil:
		t.Error("absent metadata was rendered as an empty object, which is a different claim")
	}
}

func TestEbookRendersWhatTheFileSaid(t *testing.T) {
	t.Parallel()

	work, err := ebook.New(uuid.New(),
		&ebook.Details{
			Title: "Os Sertões", Author: "Euclides da Cunha",
			Publisher: "Laemmert", Language: "pt-BR",
			Extra: ebook.Metadata{"isbn": "9788535911190"},
		},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: 1024},
		uuid.New(), at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rendered := convert.Ebook(work)

	switch {
	case rendered.GetAuthor() != "Euclides da Cunha":
		t.Errorf("the author rendered as %q", rendered.GetAuthor())
	case rendered.GetSizeBytes() != 1024:
		t.Errorf("the length rendered as %d", rendered.GetSizeBytes())
	case rendered.GetExtraMetadata().GetFields()["isbn"].GetStringValue() != "9788535911190":
		t.Error("the metadata RF05 exists for was lost")
	case rendered.GetFormat() != quirev1.EbookFormat_EBOOK_FORMAT_EPUB:
		t.Error("the format was not carried across")
	}
}

// A device absent from a clock and a device mapped to zero are the same causal
// history, and sending both forms would make them compare unequal.
func TestRevisionDropsTheZeroEntries(t *testing.T) {
	t.Parallel()

	phone, tablet := uuid.New(), uuid.New()

	rendered := convert.Revision(crdt.Revision{
		VectorClock: crdt.VectorClock{crdt.Author(phone): 2, crdt.Author(tablet): 0},
		UpdatedAt:   at,
		DeviceID:    phone,
	})

	if len(rendered.GetVectorClock().GetEntries()) != 1 {
		t.Errorf("the clock rendered as %v, want the zero entry dropped",
			rendered.GetVectorClock().GetEntries())
	}

	if rendered.GetUpdatedAt().GetUnixMicros() != at.UnixMicro() {
		t.Error("the tie-break timestamp was not carried across")
	}

	if rendered.GetDeviceId() != phone.String() {
		t.Error("the tie-break lost its second half")
	}
}

func TestCollectionRendersWhatTheReaderWrote(t *testing.T) {
	t.Parallel()

	grouping, err := collection.New(uuid.New(),
		&collection.Details{Name: "Literatura", Kind: collection.KindCategory, Description: "o que sobrou"},
		uuid.New(), at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rendered := convert.Collection(grouping)

	switch {
	case rendered.GetName() != "Literatura":
		t.Errorf("the name rendered as %q", rendered.GetName())
	case rendered.GetKind() != quirev1.CollectionKind_COLLECTION_KIND_CATEGORY:
		t.Error("the kind was not carried across")
	case rendered.GetDescription() != "o que sobrou":
		t.Error("what the reader wrote was lost")
	}
}

func TestContentRendersWhatTheBytesAre(t *testing.T) {
	t.Parallel()

	record, err := content.New(digest, 1024, "application/epub+zip",
		content.Locator{Bucket: "quire-contents", Key: "ebooks/1a/2b/" + digest}, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rendered := convert.Content(record)

	switch {
	case rendered.GetContentHash() != digest:
		t.Error("the digest a client checks its download against was not carried across")
	case rendered.GetSizeBytes() != 1024:
		t.Error("the length was not carried across")
	case rendered.GetMediaType() != "application/epub+zip":
		t.Error("the media type was not carried across")
	}
}
