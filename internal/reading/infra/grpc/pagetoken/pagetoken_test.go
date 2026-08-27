package pagetoken_test

import (
	"errors"
	"testing"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/pagetoken"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	cursor := annotation.Cursor{ID: uuid.New()}

	decoded, err := pagetoken.Decode(pagetoken.Encode(cursor))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded != cursor {
		t.Errorf("the cursor came back as %+v, want %+v", decoded, cursor)
	}
}

// An empty token is a client asking for the first page, and the first page is
// what an empty cursor asks the repository for.
func TestTheFirstPageIsSpelledAsNothing(t *testing.T) {
	t.Parallel()

	if token := pagetoken.Encode(annotation.Cursor{}); token != "" {
		t.Errorf("the end of a list rendered as %q, want no token at all", token)
	}

	decoded, err := pagetoken.Decode("")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !decoded.IsZero() {
		t.Error("an absent token was read as a position")
	}
}

// A token this node did not issue is refused rather than misread. It is an
// invalid argument and not a not-found, because a token names no entity: there
// is nothing for a refusal to reveal the existence of, and a client that sent a
// corrupted one needs to be told rather than served an empty page it would read
// as the end of the list.
func TestDecodeRefusesWhatThisNodeDidNotIssue(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"not base64":         "!!!!",
		"not a uuid":         "cGFnZT00Mg",
		"the library's form": "MTc1MDAwMDAwMDAwMDAwMDpiNzYxYzM0Ni0wZjc4LTQ0MTUtOGYxNC0wOTZiMzJmMWI4NzQ",
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := pagetoken.Decode(token); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Decode(%q) = %v, want an invalid argument", token, err)
			}
		})
	}
}
