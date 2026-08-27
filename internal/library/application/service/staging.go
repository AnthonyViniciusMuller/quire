package service

import (
	"context"
	"io"
)

// The stable machine-readable codes staging reports.
const (
	// CodeUploadTooLarge is a file longer than the node will accept.
	CodeUploadTooLarge = "upload_too_large"
	// CodeStagingFailed is the node being unable to hold the bytes while it
	// checks them.
	CodeStagingFailed = "staging_failed"
)

// Staged is a file the node has received and is holding while it decides
// whether to keep it.
//
// It is read twice: once as it arrives, to find out what it is, and once from
// the beginning, to hand it to the object store. [Staged.Rewind] is what makes
// the second read possible.
type Staged interface {
	io.Reader

	// Rewind returns the reader to the first byte.
	Rewind() error

	// Size is how many bytes arrived.
	Size() int64

	// Digest is the sha-256 of what arrived, as lowercase hexadecimal — the
	// same spelling the storage key uses.
	Digest() string

	// Close releases whatever was holding the bytes. The caller always calls
	// it, including on the path where the upload was refused.
	Close() error
}

// Staging holds an incoming file while the node checks what it is.
//
// It exists because of an ordering that cannot be avoided: the object is
// stored under the digest of its bytes, and the digest is only known once
// every byte has arrived. A node that streamed straight through to the object
// store would be writing bytes under a name that promises they are something
// else, and would have to delete them afterwards if they were not — which
// leaves the promise standing for as long as the transfer takes, and
// permanently if the node dies in between. Every later reader of that object
// trusts the name.
//
// So the bytes are received first, checked, and only then stored. What holds
// them in the meantime is this port, and the reason it is a port rather than a
// temporary file opened in the use case is that the use case would then be the
// one thing in the application layer that touches a disk.
type Staging interface {
	// Stage reads body to its end and returns what arrived.
	//
	// It stops at limit bytes and reports errs.KindResourceExhausted with
	// CodeUploadTooLarge, so that a client which declared one length and sent
	// another cannot fill the node by lying about it.
	Stage(ctx context.Context, body io.Reader, limit int64) (Staged, error)
}
