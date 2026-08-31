package beginupload

import (
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
)

// Input is a file about to be sent, described before it travels.
type Input struct {
	// UserID is the reader uploading, from the token.
	UserID uuid.UUID

	// ContentHash is the digest the client claims the bytes will have. It is
	// checked against them at the end, and it is the name they are stored
	// under.
	ContentHash string
	// Size is the length the client claims. It is checked against the ceiling
	// here, before anything arrives, which is the whole reason this call is
	// separate from the ones that carry bytes.
	Size int64
	// MediaType is what the client says the bytes are.
	MediaType string
}

// Output is the node's answer: where to send, or that there is no need to.
type Output struct {
	// UploadID names the session the chunks are sent to, and is empty when
	// AlreadyHeld is true.
	UploadID uuid.UUID

	// AlreadyHeld is true when the node already had the file. It is not an
	// error and the client need do nothing about it — the digest is the key,
	// so the bytes here are the bytes it was about to send.
	AlreadyHeld bool
	// Content is the record that says this node has the bytes, populated only
	// when AlreadyHeld is true.
	Content *content.Content
}
