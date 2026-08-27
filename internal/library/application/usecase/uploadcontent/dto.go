package uploadcontent

import (
	"io"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
)

// Input is a file arriving, described before it travels.
//
// The description comes first because the node has to be able to refuse an
// oversized or unsupported file before receiving any of it, which the contract
// says in its own words: the stream's first message is the description and
// every message after it is bytes.
type Input struct {
	// UserID is the reader uploading, from the token.
	UserID uuid.UUID

	// ContentHash is the digest the client claims the bytes have. It is
	// checked against them, and it is the name they are stored under.
	ContentHash string
	// Size is the length the client claims. It is checked against what
	// arrives, and against the ceiling before anything does.
	Size int64
	// MediaType is what the client says the bytes are.
	MediaType string

	// Body is the bytes, as the transport hands them over.
	Body io.Reader
}

// Output is the file as this node now holds it.
type Output struct {
	// Content is the record that says this node has the bytes.
	Content *content.Content

	// AlreadyHeld is true when the node already had the file and the transfer
	// was not needed. It is not an error and the client need do nothing about
	// it — the digest is the key, so the bytes here are the bytes it was
	// sending.
	AlreadyHeld bool
}
