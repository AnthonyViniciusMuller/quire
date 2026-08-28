package service

import "uuid"

// Changes is where this slice says that a reader's log has grown.
//
// It exists because of what the contract asks the sync stream to be: a change
// made on one device has to reach the reader's other devices as it happens
// rather than at the next poll. Something has to wake the streams, and the only
// call that knows the log grew is the one that grew it.
//
// It is one method on purpose. Waiting to be told is the stream's business and
// not a use case's, so the port a use case holds is the half it uses; what
// listens holds the hub itself, in internal/sync/infra/service/changes.
//
// The announcement is in-process, and the adapter's documentation says what
// that costs. It is a hint and never the mechanism: a stream that missed one
// still finds the change on its next poll, and a device that missed both finds
// it at its next pull, because the cursor is what the design actually rests on.
type Changes interface {
	// Announce reports that the reader's log has grown.
	//
	// It never fails and never blocks. A node that could not deliver a hint has
	// nothing to report and nothing to retry — the change is already committed,
	// and the cursor will carry it.
	Announce(userID uuid.UUID)
}
