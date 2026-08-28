package pushoperations

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/crdt"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
)

// Change is one operation as the caller offered it.
//
// The enumerations arrive as the strings the contract and the schema share, and
// are parsed here rather than in the controller, because what a name outside
// the set means is a rejection and a rejection is a decision. What does not
// arrive is the position: it is this node's to allocate, and the contract says
// so when it declares the field ignored in a request.
type Change struct {
	// ID is the identifier the authoring device minted, and the same value on
	// every node that ever sees the change.
	ID uuid.UUID

	// DeviceID is the device that authored it. RN10 is checked against it.
	DeviceID uuid.UUID

	// TargetEntity is which kind of record changed.
	TargetEntity string

	// TargetID is the record, as its author names it.
	TargetID uuid.UUID

	// Kind is what the change did.
	Kind string

	// Delta is the fields it wrote and nothing else.
	Delta operation.Delta

	// VectorClock is the causal version it was authored at.
	VectorClock crdt.VectorClock

	// CreatedAt is the instant its author stamped, on the clock C01 describes.
	CreatedAt time.Time
}

// Input is a batch of changes offered for one reader.
type Input struct {
	// UserID is the reader whose log they belong to. A device-facing call takes
	// it from the session; a peer-facing one takes it from the request, because
	// the certificate identifies the calling node and not any of the readers it
	// replicates.
	UserID uuid.UUID

	// Author is the device every change in the batch must be authored by, and
	// the zero value when the caller is a peer node rather than a device.
	//
	// It is the answer to who the caller is and not the credential they proved
	// it with, which is what lets one use case serve both transports. RN10 is
	// the check it exists for, and a peer has nothing to check: it replicates
	// many devices and is none of them.
	Author uuid.UUID

	// Operations are the changes, in the order they were offered.
	Operations []Change
}

// Output is one verdict per change offered, and where the log now ends.
type Output struct {
	// Results are the verdicts, in the order the changes were offered, which is
	// what lets a caller settle its own records without matching payloads.
	Results []operation.Result

	// LastPosition is this node's head position for the reader after the push.
	// A device that has just pushed learns from it whether there is anything to
	// pull, without asking.
	LastPosition int64
}
