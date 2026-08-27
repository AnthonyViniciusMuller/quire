package identifier_test

import (
	"errors"
	"testing"
	"uuid"

	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/identifier"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestDecodesWhatAClientSent(t *testing.T) {
	t.Parallel()

	id := uuid.New()

	mark, err := identifier.Annotation(id.String())
	if err != nil {
		t.Fatalf("Annotation: %v", err)
	}

	work, err := identifier.Ebook(id.String())
	if err != nil {
		t.Fatalf("Ebook: %v", err)
	}

	if mark != id || work != id {
		t.Error("an identifier was not carried across")
	}
}

// A value that is not a uuid is answered exactly as one nobody has: the reply
// must not be an oracle for which identifiers exist, and a client that sent a
// broken one learns the same thing either way.
func TestAMalformedIdentifierIsAnsweredAsOneNobodyHas(t *testing.T) {
	t.Parallel()

	_, markErr := identifier.Annotation("not-a-uuid")
	if !errors.Is(markErr, errs.KindNotFound) {
		t.Errorf("Annotation = %v, want a not found", markErr)
	}

	if got := errs.CodeOf(markErr); got != annotation.CodeNotFound {
		t.Errorf("the refusal carries %q, want the mark's own code", got)
	}

	// The work's refusal is the library slice's, taken from the entity that
	// owns it: a client that sent the same malformed value to both services
	// has to be told the same thing.
	_, workErr := identifier.Ebook("not-a-uuid")
	if !errors.Is(workErr, errs.KindNotFound) {
		t.Errorf("Ebook = %v, want a not found", workErr)
	}

	if got := errs.CodeOf(workErr); got != libraryebook.CodeNotFound {
		t.Errorf("the refusal carries %q, want the library's own code", got)
	}
}
