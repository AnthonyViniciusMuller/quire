package ebook_test

import (
	"errors"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestParseTitleTrimsAndRefusesTheUnusable(t *testing.T) {
	t.Parallel()

	title, err := ebook.ParseTitle("  Os Sertões  ")
	if err != nil {
		t.Fatalf("ParseTitle: %v", err)
	}

	if title.String() != "Os Sertões" {
		t.Errorf("the title was parsed as %q, want it trimmed", title)
	}

	cases := map[string]string{
		"empty":      "",
		"only space": "   ",
		"too long":   longString(256),
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ebook.ParseTitle(value); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("ParseTitle(%d characters) = %v, want an invalid argument", len(value), err)
			}
		})
	}
}

// The width is counted in characters and not in bytes, because that is what
// PostgreSQL measures a varchar in — a title of 255 accented characters fits
// the column and would not fit a byte count.
func TestTitleIsMeasuredInCharacters(t *testing.T) {
	t.Parallel()

	accented := ""
	for range 255 {
		accented += "é"
	}

	if _, err := ebook.ParseTitle(accented); err != nil {
		t.Errorf("a title of 255 characters was refused: %v", err)
	}
}

func TestParseFormatAcceptsExactlyWhatTheContractEnumerates(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"epub", "PDF", " mobi ", "djvu", "cbz"} {
		if _, err := ebook.ParseFormat(value); err != nil {
			t.Errorf("ParseFormat(%q): %v", value, err)
		}
	}

	for _, value := range []string{"", "txt", "epub3"} {
		if _, err := ebook.ParseFormat(value); !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("ParseFormat(%q) = %v, want an invalid argument", value, err)
		}
	}
}

// The digest is the storage key, so the same file described in upper case and
// in lower case would be two objects and the deduplication the design rests on
// would stop working without anybody noticing.
func TestParseContentHashLowercases(t *testing.T) {
	t.Parallel()

	upper := longHex('A')

	hash, err := ebook.ParseContentHash(upper)
	if err != nil {
		t.Fatalf("ParseContentHash: %v", err)
	}

	if hash.String() != longHex('a') {
		t.Errorf("the digest was parsed as %q, want it lowercased", hash)
	}
}

func TestParseContentHashRefusesWhatTheColumnWouldRefuse(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty":         "",
		"too short":     longHex('a')[:63],
		"too long":      longHex('a') + "a",
		"not hex":       longHex('z'),
		"with a prefix": "sha256:" + longHex('a')[:57],
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ebook.ParseContentHash(value); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("ParseContentHash(%q) = %v, want an invalid argument", value, err)
			}
		})
	}
}

// Zero is how absence is spelled, and the column is nullable, so it has to be
// usable — while a negative length is what library.ebooks_size_positive
// refuses.
func TestSizeAdmitsAbsenceAndRefusesTheImpossible(t *testing.T) {
	t.Parallel()

	if err := ebook.Size(0).Validate(); err != nil {
		t.Errorf("an absent length was refused: %v", err)
	}

	if !ebook.Size(0).IsZero() {
		t.Error("zero does not report itself as an absent length")
	}

	if err := ebook.Size(-1).Validate(); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("Size(-1).Validate() = %v, want an invalid argument", err)
	}
}

// longString returns n characters, for the width checks above.
func longString(n int) string {
	value := make([]byte, n)
	for index := range value {
		value[index] = 'a'
	}

	return string(value)
}

// longHex returns 64 copies of character, which is the width of a digest.
func longHex(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}

	return string(value)
}
