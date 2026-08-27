package content_test

import (
	"errors"
	"testing"
	"time"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// digest is a well-formed content hash, and stored is when this node came to
// hold the bytes.
const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

var stored = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// at is where the object store put the bytes.
func at() content.Locator {
	return content.Locator{Bucket: "quire-contents", Key: "ebooks/1a/2b/" + digest}
}

func TestNew(t *testing.T) {
	t.Parallel()

	record, err := content.New(digest, 1024, "application/epub+zip", at(), stored)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case record.Hash != digest:
		t.Error("the record is not keyed by the digest of the bytes")
	case record.Size != 1024:
		t.Errorf("the record says the file is %d bytes", record.Size)
	case record.Bucket != "quire-contents":
		t.Error("the record does not say which container the object lives in")
	case !record.StoredAt.Equal(stored):
		t.Error("the record does not say when this node came to hold the bytes")
	}
}

func TestNewRefusesARecordThatWouldPromiseAFileThisNodeCannotServe(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		hash      ebook.ContentHash
		size      int64
		mediaType content.MediaType
		at        content.Locator
		storedAt  time.Time
	}{
		"no digest":     {"", 1024, "application/epub+zip", at(), stored},
		"no media type": {digest, 1024, "", at(), stored},
		"no bytes":      {digest, 0, "application/epub+zip", at(), stored},
		"no object":     {digest, 1024, "application/epub+zip", content.Locator{}, stored},
		"no time":       {digest, 1024, "application/epub+zip", at(), time.Time{}},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := content.New(testCase.hash, testCase.size, testCase.mediaType,
				testCase.at, testCase.storedAt)
			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("New = %v, want an invalid argument", err)
			}
		})
	}
}

// Media types are case-insensitive, so storing one form is what makes a value
// read back compare equal to the one that was stored.
func TestParseMediaTypeLowercases(t *testing.T) {
	t.Parallel()

	mediaType, err := content.ParseMediaType("  Application/EPUB+Zip  ")
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}

	if mediaType != "application/epub+zip" {
		t.Errorf("the media type was parsed as %q", mediaType)
	}
}

// The value is handed back in the header of a download stream, so a media type
// with white space in it is a value that would have to be escaped somewhere or
// would corrupt something.
func TestParseMediaTypeRefusesWhatCannotTravelInAHeader(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty":            "",
		"no subtype":       "application",
		"two slashes":      "application/epub/zip",
		"nothing after":    "application/",
		"nothing before":   "/epub",
		"with a newline":   "application/epub\nx: y",
		"with a space":     "application/epub zip",
		"longer than held": "application/" + string(make([]byte, 0)) + longString(100),
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := content.ParseMediaType(value); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("ParseMediaType(%q) = %v, want an invalid argument", value, err)
			}
		})
	}
}

func TestLocatorRefusesWhatTheColumnsCannotHold(t *testing.T) {
	t.Parallel()

	cases := map[string]content.Locator{
		"no bucket":       {Bucket: "", Key: "ebooks/x"},
		"no key":          {Bucket: "quire-contents", Key: ""},
		"bucket too long": {Bucket: longString(256), Key: "ebooks/x"},
		"key too long":    {Bucket: "quire-contents", Key: longString(513)},
	}

	for name, locator := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := locator.Validate(); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Validate() = %v, want an invalid argument", err)
			}
		})
	}

	if (content.Locator{}).IsZero() != true {
		t.Error("a locator naming nothing does not report itself as one")
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
