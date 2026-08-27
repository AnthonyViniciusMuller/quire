package pagetoken_test

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/pagetoken"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	cursor := ebook.Cursor{
		ImportedAt: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC),
		ID:         uuid.New(),
	}

	decoded, err := pagetoken.Decode(pagetoken.Encode(cursor))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !decoded.ImportedAt.Equal(cursor.ImportedAt) || decoded.ID != cursor.ID {
		t.Errorf("the cursor came back as %+v, want %+v", decoded, cursor)
	}
}

// A token carrying nanoseconds would name a position between two rows, and the
// row comparison would then skip or repeat one.
func TestEncodeKeepsOnlyWhatTheColumnHolds(t *testing.T) {
	t.Parallel()

	cursor := ebook.Cursor{
		ImportedAt: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC).Add(1500 * time.Nanosecond),
		ID:         uuid.New(),
	}

	decoded, err := pagetoken.Decode(pagetoken.Encode(cursor))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !decoded.ImportedAt.Equal(cursor.ImportedAt.Truncate(time.Microsecond)) {
		t.Errorf("the cursor came back at %s, want it truncated to the microsecond", decoded.ImportedAt)
	}
}

// An empty token is a client asking for the first page, and a page that was
// the last has no token to give.
func TestTheEmptyTokenIsTheFirstPage(t *testing.T) {
	t.Parallel()

	if token := pagetoken.Encode(ebook.Cursor{}); token != "" {
		t.Errorf("a page with nothing after it issued the token %q", token)
	}

	cursor, err := pagetoken.Decode("")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !cursor.IsZero() {
		t.Error("an empty token decoded to somewhere other than the first page")
	}
}

// A client that sent a corrupted token needs to be told, rather than served an
// empty page it would read as the end of the collection.
func TestDecodeRefusesATokenThisNodeDidNotIssue(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"not base64":        "!!!!",
		"no separator":      encodeRaw("nothing to cut here"),
		"not an instant":    encodeRaw("soon:" + uuid.New().String()),
		"not an identifier": encodeRaw("1756296000000000:not-a-uuid"),
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := pagetoken.Decode(token)

			if !errors.Is(err, errs.KindInvalidArgument) {
				t.Fatalf("Decode(%q) = %v, want an invalid argument", token, err)
			}

			if code := errs.CodeOf(err); code != pagetoken.CodeInvalidPageToken {
				t.Errorf("the refusal is coded %q", code)
			}
		})
	}
}

// encodeRaw renders s the way a token is encoded, so that a test can build a
// well-encoded token whose contents this node would never have written.
func encodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
