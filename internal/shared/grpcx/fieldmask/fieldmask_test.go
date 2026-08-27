package fieldmask_test

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx/fieldmask"
)

func TestClaimedIsTheSetTheMaskNames(t *testing.T) {
	t.Parallel()

	claimed, err := fieldmask.Claimed(
		&fieldmaskpb.FieldMask{Paths: []string{"title", " author "}}, "title", "author", "language")
	if err != nil {
		t.Fatalf("Claimed: %v", err)
	}

	switch {
	case !claimed["title"]:
		t.Error("a path the mask named was not claimed")
	case !claimed["author"]:
		t.Error("a path the mask named with surrounding space was not claimed")
	case claimed["language"]:
		t.Error("a path the mask did not name was claimed, so this write would beat an edit it never saw")
	}
}

// An ignored path is a change the client believes it made, and on a per-field
// last-writer-wins entity a change nobody made stays unmade until somebody
// looks.
func TestClaimedRefusesAPathTheCallCannotWrite(t *testing.T) {
	t.Parallel()

	_, err := fieldmask.Claimed(
		&fieldmaskpb.FieldMask{Paths: []string{"title", "content_hash"}}, "title", "author")

	if !errors.Is(err, errs.KindInvalidArgument) {
		t.Fatalf("Claimed = %v, want an invalid argument", err)
	}

	if code := errs.CodeOf(err); code != fieldmask.CodeInvalidFieldMask {
		t.Errorf("the refusal is coded %q", code)
	}

	if fields := errs.FieldsOf(err); len(fields) != 1 || fields[0].Name != "update_mask" {
		t.Errorf("the refusal points at %v, want the mask", fields)
	}
}

// Whether a write that claims nothing is an error is the use case's decision,
// not this package's: only it knows whether the write is worth a revision.
func TestClaimedAcceptsAnAbsentMask(t *testing.T) {
	t.Parallel()

	claimed, err := fieldmask.Claimed(nil, "title")
	if err != nil {
		t.Fatalf("Claimed: %v", err)
	}

	if len(claimed) != 0 {
		t.Errorf("an absent mask claimed %v", claimed)
	}
}
