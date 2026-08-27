package locator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestParseKeepsWhatTheClientMeant(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"an epub canonical fragment": "epubcfi(/6/14[chap05ref]!/4[body01]/10[para05]/3:10)",
		"a page in a pdf":            "page=42",
		"surrounding space":          "  page=42  ",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parsed, err := locator.Parse(value)
			if err != nil {
				t.Fatalf("Parse(%q): %v", value, err)
			}

			if parsed.String() != strings.TrimSpace(value) {
				t.Errorf("Parse(%q) = %q, want the value with only its surrounding space removed",
					value, parsed)
			}
		})
	}
}

func TestParseRefusesWhatTheColumnCannotHold(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"nothing":        "",
		"only space":     "   ",
		"too many runes": strings.Repeat("é", 256),
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := locator.Parse(value); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Parse(%q) = %v, want an invalid argument", value, err)
			}
		})
	}
}

// The constraint tests btrim(locator), so a locator of three spaces is blank
// to the database and has to be refused here rather than by the driver.
func TestValidateRefusesWhatTheConstraintCallsBlank(t *testing.T) {
	t.Parallel()

	if err := locator.Locator("   ").Validate(); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Validate = %v, want an invalid argument", err)
	}
}

// The column counts characters and not bytes, so a locator of 255 accented
// characters fits and one of 256 does not, whatever either weighs.
func TestValidateCountsCharacters(t *testing.T) {
	t.Parallel()

	if err := locator.Locator(strings.Repeat("é", 255)).Validate(); err != nil {
		t.Errorf("a locator of 255 characters was refused: %v", err)
	}
}
