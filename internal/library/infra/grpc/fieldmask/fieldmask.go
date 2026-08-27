// Package fieldmask reads the update mask of a request into the set of fields
// a write claims.
//
// The mask is not a convenience in this contract. Reconciliation is per-field
// last-writer-wins, so a mask naming two fields is a claim over those two and
// leaves every other field to whichever device wrote it last — which means the
// difference between a field the client named and one it merely sent is the
// difference between winning and losing a tie-break against a device this one
// has never seen.
//
// That is why an unknown path is refused rather than ignored. An ignored path
// is a change the client believes it made, and on this kind of entity a change
// nobody made is a change that stays unmade until somebody looks.
package fieldmask

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opClaimed is the operation reported by this file, in the form the errs
// package expects.
const opClaimed = "library/fieldmask: claimed"

// CodeInvalidFieldMask is a mask naming a path the call cannot write.
const CodeInvalidFieldMask = "invalid_field_mask"

// Claimed returns the set of paths the mask names, refusing any that is not in
// writable.
//
// An absent or empty mask claims nothing, and the use case is what decides
// whether that is an error. Keeping the decision there is deliberate: this
// package knows which paths exist, and only the use case knows whether a write
// that claims none is one worth stamping a revision for.
func Claimed(mask *fieldmaskpb.FieldMask, writable ...string) (map[string]bool, error) {
	claimed := make(map[string]bool, len(writable))

	for _, path := range mask.GetPaths() {
		trimmed := strings.TrimSpace(path)

		if !slices.Contains(writable, trimmed) {
			return nil, errs.Newf(errs.KindInvalidArgument,
				"the update mask names %q, which this call cannot write", trimmed).
				WithOp(opClaimed).
				WithCode(CodeInvalidFieldMask).
				WithField("update_mask", "it may name "+strings.Join(writable, ", "))
		}

		claimed[trimmed] = true
	}

	return claimed, nil
}
