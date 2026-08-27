package convert_test

import (
	"testing"
	"time"
	"uuid"

	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/convert"
)

// at is the instant the records below were stamped at.
var at = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func TestAnnotationRendersWhatTheReaderWrote(t *testing.T) {
	t.Parallel()

	work, phone := uuid.New(), uuid.New()

	mark, err := annotation.New(work, &annotation.Mark{
		Kind:    annotation.KindNote,
		Text:    "a sertão é uma sociedade",
		Locator: locator.Locator("epubcfi(/6/14!/4/10/3:10)"),
	}, phone, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rendered := convert.Annotation(mark)

	switch {
	case rendered.GetEbookId() != work.String():
		t.Error("the work the mark is in was not carried across")
	case rendered.GetKind() != quirev1.AnnotationKind_ANNOTATION_KIND_NOTE:
		t.Error("the kind was not carried across")
	case rendered.GetText() != "a sertão é uma sociedade":
		t.Errorf("the text rendered as %q", rendered.GetText())
	case rendered.GetLocator() != "epubcfi(/6/14!/4/10/3:10)":
		t.Errorf("the passage rendered as %q", rendered.GetLocator())
	case rendered.GetRevision().GetDeviceId() != phone.String():
		t.Error("the revision lost the device whose write the row reflects")
	}
}

// A highlight the reader commented and one they did not are different claims,
// and the contract makes the same distinction.
func TestAnnotationRendersAbsentTextAsAbsent(t *testing.T) {
	t.Parallel()

	mark, err := annotation.New(uuid.New(), &annotation.Mark{
		Kind:    annotation.KindHighlight,
		Locator: locator.Locator("page=42"),
	}, uuid.New(), at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if convert.Annotation(mark).Text != nil {
		t.Error("a highlight with no comment rendered as one carrying the empty string")
	}
}

// A mark replicated from a node running a later version keeps its row and can
// still be stored, returned and read; the only thing this node cannot do is
// name the kind to its own reader.
func TestKindOfAValueThisNodeDoesNotKnow(t *testing.T) {
	t.Parallel()

	if got := convert.Kind("underline"); got != quirev1.AnnotationKind_ANNOTATION_KIND_UNSPECIFIED {
		t.Errorf("an unknown kind rendered as %v", got)
	}
}

// UNSPECIFIED becomes the empty string rather than a default, so that a client
// which left the field out is refused by the value object instead of having a
// kind chosen for it — a mark whose kind was guessed is a mark the reader did
// not make.
func TestKindValueChoosesNothingForAClientThatSaidNothing(t *testing.T) {
	t.Parallel()

	if got := convert.KindValue(quirev1.AnnotationKind_ANNOTATION_KIND_UNSPECIFIED); got != "" {
		t.Errorf("an unspecified kind read back as %q", got)
	}

	if got := convert.KindValue(quirev1.AnnotationKind_ANNOTATION_KIND_BOOKMARK); got != "bookmark" {
		t.Errorf("a bookmark read back as %q", got)
	}
}

// The clock and the timestamp are rendered side by side rather than inside a
// Revision, as the contract has them: this row has one writer, so there is no
// tie-break for a Revision to hold (C05).
func TestProgressCarriesTheVersionAndNoTieBreak(t *testing.T) {
	t.Parallel()

	work, phone := uuid.New(), uuid.New()

	percent, err := progress.NewPercent(40)
	if err != nil {
		t.Fatalf("NewPercent: %v", err)
	}

	position, err := progress.New(work, phone,
		&progress.Position{Locator: locator.Locator("page=42"), Percent: percent}, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rendered := convert.Progress(position)

	switch {
	case rendered.GetEbookId() != work.String() || rendered.GetDeviceId() != phone.String():
		t.Error("the pair that addresses the row was not carried across")
	case rendered.GetLocator() != "page=42":
		t.Errorf("the position rendered as %q", rendered.GetLocator())
	case rendered.GetPercent() != 40:
		t.Errorf("the proportion rendered as %v", rendered.GetPercent())
	case rendered.GetUpdatedAt().GetUnixMicros() != at.UnixMicro():
		t.Error("the version timestamp was not carried across")
	case len(rendered.GetVectorClock().GetEntries()) != 1:
		t.Errorf("the clock rendered as %v, want the one device that may write the row",
			rendered.GetVectorClock().GetEntries())
	}
}

// Absent means the client could not compute a proportion, which is a different
// claim from a reader who has read none of the work.
func TestProgressRendersAnAbsentProportionAsAbsent(t *testing.T) {
	t.Parallel()

	position, err := progress.New(uuid.New(), uuid.New(),
		&progress.Position{Locator: locator.Locator("page=1"), Percent: progress.NoPercent()}, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if convert.Progress(position).Percent != nil {
		t.Error("a client that computed no proportion had one rendered for it")
	}
}
