package service

import (
	"context"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
)

// The stable machine-readable codes an upload session reports.
const (
	// CodeNoSuchUpload is a session this node is not holding: one that was
	// never begun, one that has been finished, or one that expired while
	// nobody was sending to it.
	CodeNoSuchUpload = "no_such_upload"
	// CodeTooManyUploads is a reader with more sessions open than the node
	// will hold for one.
	CodeTooManyUploads = "too_many_uploads"
)

// Declared is what a caller said it was going to send.
//
// It is deliberately not a [Blob]. A Blob is what goes to the object store,
// and it is built only once the bytes have been checked against this — naming
// them the same thing would put the unverified case and the verified one under
// one word, which is the confusion the whole staging ordering exists to avoid.
type Declared struct {
	// Hash is the digest the caller says the bytes will have. Nothing is
	// stored under it until the node has computed the same digest itself.
	Hash ebook.ContentHash
	// Size is how many bytes the caller says will arrive. It is checked
	// against the node's ceiling before any of them do, which is the ordering
	// UC02 already depends on.
	Size int64
	// MediaType is what the caller says the file is.
	MediaType content.MediaType
}

// Upload is a file arriving across several calls, as the caller sees it.
type Upload struct {
	// ID names the session, and is what the caller sends with every chunk.
	ID uuid.UUID
	// Declared is what the session was begun for, held here so that the check
	// at the end is made against what was checked at the beginning. A caller
	// that could restate it at the end could declare a small file, pass the
	// ceiling, and then finish a large one.
	Declared Declared
	// Received is how many bytes have arrived, and therefore the offset the
	// next chunk must carry.
	Received int64
}

// Finished is what a completed session hands over.
type Finished struct {
	// Declared is what the session was begun for.
	Declared Declared
	// Staged is what arrived, ready to be checked and stored exactly as a
	// file that arrived in one stream would be. The caller owns it and closes
	// it.
	Staged Staged
}

// Uploads holds files that arrive across several calls rather than in one
// stream.
//
// It exists for UC02 and for one caller: a browser, which cannot open a client
// stream because gRPC-Web does not carry one (D10). D11 records what the shape
// costs, and the cost is this port — the node holds a half-received file
// between two calls of one reader, which it does not otherwise do.
//
// The state is in the process, because the bytes are: staging holds them in a
// file it unlinks the moment it opens, so the descriptor cannot be reopened by
// name and the session cannot be picked up by another replica. That is
// affordable because the node already runs one replica, for a reason that has
// nothing to do with uploads, and it is recorded in the deployment beside that
// one.
//
// # Whose a session is
//
// Every method takes the reader the call was made by, and a session belongs to
// exactly one. A caller naming somebody else's session is answered as though it
// did not exist, rather than being told it may not have it: the identifier is
// the only thing a stranger would have to guess, and a refusal that
// distinguishes "not yours" from "not here" tells them when they have guessed
// right.
type Uploads interface {
	// Begin opens a session for a file the reader is about to send.
	//
	// It records what was declared and nothing else; the checks that belong to
	// UC02 — the ceiling, the media type, and the C16 precondition that a work
	// of the caller's already names the digest — are the use case's, and are
	// made before this is called.
	Begin(ctx context.Context, owner uuid.UUID, declared Declared) (*Upload, error)

	// Append writes a chunk at an offset and reports where the session now is.
	//
	// A chunk whose offset is not what the node is expecting is not written,
	// and the answer carries the offset it does expect. That is what makes the
	// upload resumable where the stream it replaces is not: a caller whose
	// connection died continues from what the node has rather than from the
	// beginning, and a caller that lost an answer and sent the same chunk
	// twice is told so instead of corrupting the file.
	Append(ctx context.Context, owner, id uuid.UUID, offset int64, chunk []byte) (*Upload, error)

	// Finish ends the session and hands over what arrived.
	//
	// It does not check the bytes. Whether they are the declared length and
	// the declared digest is UC02's question and is answered where it already
	// is, against the same staged file a streamed upload produces.
	Finish(ctx context.Context, owner, id uuid.UUID) (*Finished, error)

	// Discard ends a session the caller is abandoning, and releases the bytes.
	//
	// A caller that never calls it is not a leak: the sweeper ends a session
	// nobody has sent to for long enough, which is what covers the client that
	// closed its laptop rather than its upload.
	Discard(ctx context.Context, owner, id uuid.UUID) error
}
