package crdt

// Merging is where the whole design pays out, and it is worth stating in one
// place what the two functions below are and what they rest on.
//
// A replicated record is a value in a join semilattice, and reconciling two
// copies of it is taking their join. The vector clock decides first (RN02): if
// one version causally precedes the other, the later one is the join and
// nothing else is consulted. Only when the clocks report the two concurrent —
// each replica observed something the other did not — is a tie-break needed,
// and it is (UpdatedAt, DeviceID), which C01 in docs/tcc-corrections.md
// establishes as a total order.
//
// That it is a total order is the whole argument. Taking a join over a set of
// versions is taking a maximum, a maximum exists only if the relation has no
// cycle, and C01's counterexample is a cycle built out of three ordinary
// writes with a wall clock in the timestamp. The relation is acyclic precisely
// because every edge points at a larger timestamp: the concurrent edges do by
// the tie-break itself, and the causal edges do because the timestamp is
// stamped on a hybrid logical clock, so a write that observed another carries
// a later instant than it.
//
// The property tests in merge_laws_test.go check the consequence rather than
// the premise: over histories built the way the clock builds them, the join is
// commutative, associative and idempotent, and a set of versions reduces to
// the same survivor whatever order and grouping it is reduced in. One of them
// runs C01's counterexample with a wall clock instead, and shows the same
// reduction landing on two different answers — which is what the correction
// exists to remove and what this file assumes has been removed.

// Supersedes reports whether this revision is the one that survives a merge
// with other.
//
// It is false for two revisions that describe the same version, which is what
// makes it a strict order: an operation that adds nothing to what the record
// already reflects changes nothing, and the contract answers it as superseded
// rather than as applied.
func (r Revision) Supersedes(other Revision) bool {
	switch r.VectorClock.Compare(other.VectorClock) {
	case After:
		return true
	case Before, Equal:
		return false
	case Concurrent:
		return r.beatsOnTheTieBreak(other)
	default:
		return false
	}
}

// beatsOnTheTieBreak settles two versions the causal order reports concurrent.
//
// The instant decides, and the device settles the instants that are equal —
// which happens, because two devices that have never heard of each other can
// still stamp the same microsecond. Any fixed rule works provided every node
// applies the same one, and the identifier is compared as the sixteen bytes it
// is: that is the same order as its canonical text, and it is an order a node
// cannot get wrong by rendering the value differently.
func (r Revision) beatsOnTheTieBreak(other Revision) bool {
	if !r.UpdatedAt.Equal(other.UpdatedAt) {
		return r.UpdatedAt.After(other.UpdatedAt)
	}

	return r.DeviceID.Compare(other.DeviceID) > 0
}

// Merge returns whichever of the two revisions survives.
//
// It is the join of the semilattice, and the reason it can be written as a
// choice rather than as a combination is that a revision describes one
// version of a whole record: the fields of the loser are not folded into the
// winner, because they belong to a version this one supersedes.
func (r Revision) Merge(other Revision) Revision {
	if other.Supersedes(r) {
		return other
	}

	return r
}

// Supersedes reports whether this version is the one that survives a merge
// with other.
//
// There is no tie-break, and that is C05 rather than an omission: a record
// carrying a [Version] has exactly one writer, its writes are totally ordered
// by that writer's own counter, and two of its versions can therefore never be
// concurrent. A pair that somehow is concurrent is not a tie to break but a
// row written by a device that had no business writing it, and answering it
// with "neither supersedes" is what leaves the record alone until somebody
// looks.
func (v Version) Supersedes(other Version) bool {
	return v.VectorClock.HappensAfter(other.VectorClock)
}

// Merge returns whichever of the two versions survives.
func (v Version) Merge(other Version) Version {
	if other.Supersedes(v) {
		return other
	}

	return v
}
