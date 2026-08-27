package downloadcontent

import (
	"io"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
)

// Input names the work whose bytes the reader wants.
type Input struct {
	// UserID is the reader asking, from the token.
	UserID uuid.UUID
	// EbookID is the work. The bytes are addressed through it rather than by
	// digest, because a digest is not a thing a reader owns: the object is
	// shared between every work that names it, and the work is what says this
	// reader may have it.
	EbookID uuid.UUID
}

// Output is the file, opened.
type Output struct {
	// Content is what the bytes are: their digest, their length and their
	// media type, which is what the first message of the stream carries.
	Content *content.Content

	// Body is the bytes. The caller closes it, including when it stops reading
	// early because the client hung up.
	Body io.ReadCloser
}
