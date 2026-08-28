package operation

import "uuid"

// Outcome is what became of one operation a caller offered.
//
// The four are the contract's, and they are four rather than two because
// "stored" and "changed the record" are different facts about the same
// operation. A node that answered only success or failure would make a
// superseded write look like an applied one to the device that made it, and a
// duplicate look like a fault.
type Outcome uint8

// The outcomes, in the order the contract enumerates them.
const (
	// OutcomeUnspecified is the zero value, and is never a verdict.
	OutcomeUnspecified Outcome = iota

	// OutcomeApplied means the operation was stored and it changed the
	// record.
	OutcomeApplied

	// OutcomeDuplicate means this node already had the operation, by the
	// identifier its author minted. Nothing was done and nothing is wrong: an
	// operation reaching a node twice by two routes is the normal shape of a
	// federation.
	OutcomeDuplicate

	// OutcomeSuperseded means the operation was stored and lost the merge.
	// The record already held a version this one does not causally precede
	// and does not beat on the tie-break, so the operation is kept in the log
	// — a later node must reach the same conclusion from the same history —
	// and the record is unchanged.
	OutcomeSuperseded

	// OutcomeRejected means the operation was refused. The detail says why: a
	// target that does not exist here, a field the record cannot hold, an
	// entity name this node does not know. A caller cannot fix most of these
	// by retrying.
	OutcomeRejected
)

// String renders the outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeUnspecified:
		return "unspecified"
	case OutcomeApplied:
		return "applied"
	case OutcomeDuplicate:
		return "duplicate"
	case OutcomeSuperseded:
		return "superseded"
	case OutcomeRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// Verdict is what became of one operation, and why when it was refused.
type Verdict struct {
	// Outcome is the verdict itself.
	Outcome Outcome

	// Detail is populated for a rejection, and is for the operator rather than
	// the reader: what is wrong with a replicated operation is a fact about
	// two nodes and not about anything the reader did.
	Detail string
}

// Applied is the verdict on an operation that changed the record.
func Applied() Verdict { return Verdict{Outcome: OutcomeApplied} }

// Duplicate is the verdict on an operation this node already had.
func Duplicate() Verdict { return Verdict{Outcome: OutcomeDuplicate} }

// Superseded is the verdict on an operation that lost the merge.
func Superseded() Verdict { return Verdict{Outcome: OutcomeSuperseded} }

// Rejected is the verdict on an operation this node refused, with the reason.
func Rejected(detail string) Verdict {
	return Verdict{Outcome: OutcomeRejected, Detail: detail}
}

// Result is the verdict on one operation, named.
//
// The reply carries one of these per operation offered, in the order they were
// offered, which is what lets a caller settle its delivery rows without
// matching payloads.
type Result struct {
	// OperationID is the identifier its author minted.
	OperationID uuid.UUID

	Verdict
}
