package finishupload

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
)

// Input names the session to end.
type Input struct {
	// UserID is the reader uploading, from the token.
	UserID uuid.UUID
	// UploadID names the session, from the call that began it.
	UploadID uuid.UUID
}

// Output is the file as this node now holds it.
type Output struct {
	// Content is the record that says this node has the bytes.
	Content *content.Content
	// AlreadyHeld is true when another upload of the same file finished while
	// this one was arriving.
	AlreadyHeld bool
}
