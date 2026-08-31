package discardupload

import "uuid"

// Input names the session to abandon.
type Input struct {
	// UserID is the reader uploading, from the token.
	UserID uuid.UUID
	// UploadID names the session, from the call that began it.
	UploadID uuid.UUID
}

// Output carries nothing: an upload that has been abandoned leaves the caller
// with nothing to learn.
type Output struct{}
