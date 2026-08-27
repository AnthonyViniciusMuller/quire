package annotation_test

import (
	"errors"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestParseKind(t *testing.T) {
	t.Parallel()

	// A kind is lowercased and trimmed on the way in, so the three spellings
	// a client might send arrive as one.
	tests := map[string]annotation.Kind{
		"note":       annotation.KindNote,
		"HIGHLIGHT":  annotation.KindHighlight,
		"\tbookmark": annotation.KindBookmark,
	}

	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			got, err := annotation.ParseKind(value)
			if err != nil {
				t.Fatalf("ParseKind(%q): %v", value, err)
			}

			if got != want {
				t.Errorf("ParseKind(%q) = %q, want %q", value, got, want)
			}
		})
	}
}

// The wire carries the kind as an enum, so a value outside the set could not
// have come from a client of this contract — and the column refuses it in any
// case.
func TestParseKindRefusesWhatTheColumnRefuses(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "   ", "underline", "note "} {
		if _, err := annotation.ParseKind(value); value == "note " {
			if err != nil {
				t.Errorf("ParseKind(%q): %v", value, err)
			}
		} else if !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("ParseKind(%q) = %v, want an invalid argument", value, err)
		}
	}
}

// The constraint tests btrim(text), so a note of three spaces is a note of
// nothing to the database and has to be one here as well.
func TestParseTextTrimsWhatTheConstraintTrims(t *testing.T) {
	t.Parallel()

	if got := annotation.ParseText("   "); !got.IsZero() {
		t.Errorf("ParseText(%q) = %q, want the empty text the constraint sees", "   ", got)
	}

	if got := annotation.ParseText("  uma nota  "); got.String() != "uma nota" {
		t.Errorf("ParseText = %q, want its surrounding space removed", got)
	}
}
