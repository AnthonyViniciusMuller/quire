package crdt

// Ordering is the causal relation between two vector clocks, as established by
// [VectorClock.Compare].
type Ordering uint8

// The four possible causal relations. They are exhaustive: any two vector
// clocks stand in exactly one of them.
const (
	// Equal means both clocks observed exactly the same events. Neither
	// replica knows anything the other does not.
	Equal Ordering = iota
	// Before means the receiver happened before the argument: every event it
	// observed, the argument observed too, and the argument observed more.
	Before
	// After is Before seen from the other side.
	After
	// Concurrent means each clock observed at least one event the other did
	// not. This is the conflict that reconciliation exists to resolve, and the
	// only case where a deterministic tie-break is needed.
	Concurrent
)

// String returns the name of the relation.
func (o Ordering) String() string {
	switch o {
	case Equal:
		return "equal"
	case Before:
		return "before"
	case After:
		return "after"
	case Concurrent:
		return "concurrent"
	default:
		return "unknown"
	}
}

// Reverse returns the relation seen from the other operand, so that
// a.Compare(b) == b.Compare(a).Reverse() always holds.
func (o Ordering) Reverse() Ordering {
	switch o {
	case Before:
		return After
	case After:
		return Before
	case Equal, Concurrent:
		return o
	default:
		return o
	}
}
