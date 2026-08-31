package putchunk

import "uuid"

// Input is one piece of a file being sent.
type Input struct {
	// UserID is the reader uploading, from the token. A session belongs to
	// one, and a caller naming somebody else's is answered as though it did
	// not exist.
	UserID uuid.UUID

	// UploadID names the session, from the call that began it.
	UploadID uuid.UUID
	// Offset is where these bytes belong, counted from the first byte of the
	// file.
	Offset int64
	// Chunk is the bytes.
	Chunk []byte
}

// Output is where the session is now.
type Output struct {
	// ReceivedBytes is how many bytes the node holds, and the offset the next
	// chunk must carry. A caller that finds it unchanged sent a chunk the node
	// was not expecting.
	ReceivedBytes int64
}
