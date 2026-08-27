package service_test

import (
	"strings"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// digest is a well-formed content hash, which is what a key is derived from.
const digest = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

func TestObjectKeyFansOutByPrefix(t *testing.T) {
	t.Parallel()

	key := service.ObjectKey(ebook.ContentHash(digest))

	if want := "ebooks/1a/2b/" + digest; key != want {
		t.Errorf("ObjectKey = %q, want %q", key, want)
	}

	if !strings.HasSuffix(key, digest) {
		t.Error("the key does not end in the digest, so an object cannot be traced back to the file it is")
	}
}

// The layout has to be the same whichever adapter wrote the object: a bucket
// written by a node configured for MinIO and read by the same node configured
// for the S3 API of the same store is the ordinary way a deployment moves.
func TestObjectKeyIsDeterministic(t *testing.T) {
	t.Parallel()

	first := service.ObjectKey(ebook.ContentHash(digest))
	second := service.ObjectKey(ebook.ContentHash(digest))

	if first != second {
		t.Errorf("the same digest produced %q and then %q", first, second)
	}
}

// A hash too short to fan out cannot reach the store — the value object
// refuses it — but the function is total, and a panic here would be a panic in
// the middle of an upload.
func TestObjectKeyToleratesADigestTooShortToFanOut(t *testing.T) {
	t.Parallel()

	if key := service.ObjectKey("ab"); key != "ebooks/ab" {
		t.Errorf("ObjectKey(%q) = %q", "ab", key)
	}
}
