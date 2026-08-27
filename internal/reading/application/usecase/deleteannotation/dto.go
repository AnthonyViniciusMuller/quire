package deleteannotation

import "uuid"

// Input is the mark to tombstone.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// DeviceID is the appliance making the deletion, from the token. It is
	// what the tombstone names, because a deletion is a write like any other.
	DeviceID uuid.UUID
	// AnnotationID is the mark.
	AnnotationID uuid.UUID
}

// Output is empty: the contract's DeleteAnnotationResponse carries nothing.
//
// It is a struct rather than a nothing type because the use case shape of the
// slice takes one, and because a later version that wanted to report the
// tombstone's revision would add a field rather than a signature.
type Output struct{}
