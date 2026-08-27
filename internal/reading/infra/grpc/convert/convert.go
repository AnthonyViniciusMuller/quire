// Package convert translates between the messages of the reading contract and
// the vocabulary of the use cases.
//
// It is one package rather than a method on each entity, because the direction
// is the point: the domain must not know that a protobuf exists. Everything
// here reads a value on one side and writes one on the other, and nothing here
// decides anything — a controller that needed a decision would be a use case
// written in the wrong place.
//
// It translates both ways, and the reason is the enumeration. A kind of mark is
// named on the wire and stored as text, and the mapping between the two
// spellings has to exist exactly once or a note recorded as ANNOTATION_KIND_NOTE
// comes back as something else.
//
// The causal metadata is not rendered here. Both entities carry some and it goes
// on the wire the same way for every slice, so it comes from
// internal/shared/crdtpb — see that package for why one definition of it matters
// more than the convenience of a local one.
package convert

import (
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/crdtpb"
)

// Kind renders a stored kind as the enumerator the contract names it by.
//
// A value this node does not know becomes UNSPECIFIED rather than an error. The
// reasoning is the library slice's for a format: adding a kind is not a
// breaking change, so a mark replicated from a node running a later version
// keeps its row and can still be stored, returned and read — the only thing
// this node cannot do is name the kind to its own reader.
func Kind(kind annotation.Kind) quirev1.AnnotationKind {
	switch kind {
	case annotation.KindNote:
		return quirev1.AnnotationKind_ANNOTATION_KIND_NOTE
	case annotation.KindHighlight:
		return quirev1.AnnotationKind_ANNOTATION_KIND_HIGHLIGHT
	case annotation.KindBookmark:
		return quirev1.AnnotationKind_ANNOTATION_KIND_BOOKMARK
	default:
		return quirev1.AnnotationKind_ANNOTATION_KIND_UNSPECIFIED
	}
}

// KindValue reads an enumerator back into what is stored.
//
// UNSPECIFIED becomes the empty string rather than a default, so that a client
// which left the field out is refused by the value object instead of having a
// kind chosen for it — a mark whose kind was guessed is a mark the reader did
// not make.
func KindValue(kind quirev1.AnnotationKind) string {
	switch kind {
	case quirev1.AnnotationKind_ANNOTATION_KIND_NOTE:
		return annotation.KindNote.String()
	case quirev1.AnnotationKind_ANNOTATION_KIND_HIGHLIGHT:
		return annotation.KindHighlight.String()
	case quirev1.AnnotationKind_ANNOTATION_KIND_BOOKMARK:
		return annotation.KindBookmark.String()
	case quirev1.AnnotationKind_ANNOTATION_KIND_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// Annotation renders one mark.
func Annotation(mark *annotation.Annotation) *quirev1.Annotation {
	rendered := &quirev1.Annotation{
		Id:       mark.ID.String(),
		EbookId:  mark.EbookID.String(),
		Kind:     Kind(mark.Kind),
		Locator:  mark.Locator.String(),
		Revision: crdtpb.Revision(mark.Revision),
	}

	// The text is rendered as absent rather than as an empty string. A
	// highlight the reader commented and one they did not are different
	// claims, and the contract makes the same distinction.
	if !mark.Text.IsZero() {
		text := mark.Text.String()
		rendered.Text = &text
	}

	return rendered
}

// Annotations renders a page of marks.
func Annotations(marks []*annotation.Annotation) []*quirev1.Annotation {
	rendered := make([]*quirev1.Annotation, 0, len(marks))

	for _, mark := range marks {
		rendered = append(rendered, Annotation(mark))
	}

	return rendered
}

// Progress renders where one device stopped.
//
// The clock and the timestamp are rendered side by side rather than inside a
// Revision, as the contract has them: this row has one writer, so it carries no
// tie-break and there is nothing for a Revision to hold (C05 in
// docs/tcc-corrections.md). The device is a field of the message because it is
// half of the row's key, not because it settles anything.
func Progress(position *progress.Progress) *quirev1.ReadingProgress {
	rendered := &quirev1.ReadingProgress{
		EbookId:     position.EbookID.String(),
		DeviceId:    position.DeviceID.String(),
		Locator:     position.Locator.String(),
		VectorClock: crdtpb.VectorClock(position.Version.VectorClock),
		UpdatedAt:   crdtpb.Timestamp(position.Version.UpdatedAt),
	}

	// Absent means the client could not compute a proportion, which is a
	// different claim from a reader who has read none of the work.
	if position.Percent.IsKnown() {
		percent := position.Percent.Float64()
		rendered.Percent = &percent
	}

	return rendered
}

// ProgressList renders every device's position in one work.
func ProgressList(positions []*progress.Progress) []*quirev1.ReadingProgress {
	rendered := make([]*quirev1.ReadingProgress, 0, len(positions))

	for _, position := range positions {
		rendered = append(rendered, Progress(position))
	}

	return rendered
}
