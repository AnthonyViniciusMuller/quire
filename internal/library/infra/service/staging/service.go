// Package staging holds an incoming file on local disk while the node checks
// what it is.
//
// A temporary file and not a buffer in memory: the bound is the configured
// upload ceiling, which is half a gigabyte by default, and a node that held
// that per concurrent upload would be a node whose memory is decided by its
// clients.
//
// The file is unlinked as soon as it is opened, so that the bytes are reachable
// through the descriptor and through nothing else. A node that is killed
// mid-upload therefore leaves nothing behind — there is no name in the
// directory to leave — and no other process on the machine can read a reader's
// book out of a temporary directory while it is being received.
package staging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opStage  = "library/staging: stage"
	opOpen   = "library/staging: open"
	opAppend = "library/staging: append"
	opRead   = "library/staging: read"
	opRewind = "library/staging: rewind"
)

// namePattern is what the temporary file is called for the moment it has a
// name at all.
const namePattern = "quire-upload-*"

// Service stages uploads on local disk.
type Service struct{}

// Service satisfies the port the use cases hold.
var _ service.Staging = (*Service)(nil)

// New returns the adapter.
func New() *Service { return &Service{} }

// Stage reads body to its end and returns what arrived.
func (*Service) Stage(_ context.Context, body io.Reader, limit int64) (service.Staged, error) {
	staged, err := hold(opStage)
	if err != nil {
		return nil, err
	}

	// One byte past the ceiling, so that a file exactly at it is accepted and
	// the first one over is refused without being read to its end.
	written, err := io.Copy(io.MultiWriter(staged.file, staged.digest), io.LimitReader(body, limit+1))
	if err != nil {
		_ = staged.Close()

		return nil, arrivalFailed(err)
	}

	if written > limit {
		_ = staged.Close()

		return nil, errs.New(errs.KindResourceExhausted, "the file is larger than this node accepts").
			WithOp(opStage).
			WithCode(service.CodeUploadTooLarge).
			WithField("size_bytes", "it must be at most what the node was configured to hold")
	}

	staged.size = written

	return staged, nil
}

// arrivalFailed is the error for a stream that ended before its end.
//
// The stream is the caller's, and the caller may already have said what went
// wrong: the controller that feeds a gRPC stream in here reports a message
// that described the file twice as the client's mistake and a caller that
// hung up as a cancellation. Those are kept as they are. Wrapping them would
// replace the kind — errs.KindOf reads the outermost — and turn the client's
// own mistake into a node that is unavailable, in the reply and in the logs.
// A bare cancellation is classified the same way; anything else is a transfer
// that was lost, which is the transport's failure and not the node's.
func arrivalFailed(err error) error {
	var already *errs.Error
	if errors.As(err, &already) {
		return err
	}

	kind := errs.KindOf(err)
	if kind == errs.KindUnknown {
		kind = errs.KindUnavailable
	}

	return errs.Wrap(err, kind, "the upload did not arrive in full").
		WithOp(opStage).
		WithCode(service.CodeStagingFailed)
}

// Open returns a holder the caller fills a chunk at a time.
func (*Service) Open(_ context.Context, limit int64) (service.Incoming, error) {
	staged, err := hold(opOpen)
	if err != nil {
		return nil, err
	}

	return &incoming{upload: staged, limit: limit}, nil
}

// hold creates the unlinked temporary file both shapes of staging write into.
//
// The unlink is immediate and is the point: the descriptor stays valid, and
// from there the bytes have no name anything else on the machine could open. A
// node killed mid-upload leaves nothing behind, because there is nothing in a
// directory to leave.
func hold(op string) (*upload, error) {
	file, err := os.CreateTemp("", namePattern)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the node could not hold the incoming file").
			WithOp(op).
			WithCode(service.CodeStagingFailed)
	}

	if err = os.Remove(file.Name()); err != nil {
		_ = file.Close()

		return nil, errs.Wrap(err, errs.KindInternal, "the node could not hold the incoming file").
			WithOp(op).
			WithCode(service.CodeStagingFailed)
	}

	return &upload{file: file, digest: sha256.New()}, nil
}

// incoming is a staged file being filled a chunk at a time.
//
// It is the same held file as a streamed upload, written to by a caller that
// has the bytes one call at a time instead of read from a stream that has them
// all. The hashing is the same hashing, done as the bytes arrive, which is why
// the two shapes of UC02 produce a staged file the rest of the flow cannot
// tell apart.
//
// It carries no lock. One session is written to by one call at a time, and
// what serializes them is the registry that hands it out — a second caller
// with the same identifier finds it in use rather than racing this one.
type incoming struct {
	upload *upload
	limit  int64
	done   bool
}

// incoming satisfies the port's view of a file arriving in pieces.
var _ service.Incoming = (*incoming)(nil)

// Append writes the next bytes and reports how many have arrived in all.
func (i *incoming) Append(chunk []byte) (int64, error) {
	if i.done {
		return i.upload.size, errs.New(errs.KindFailedPrecondition,
			"the upload has already been finished").
			WithOp(opAppend).
			WithCode(service.CodeStagingFailed)
	}

	// Checked before the write and against the total, so that the file on disk
	// never exceeds the ceiling even briefly — a caller cannot spend the
	// node's disk on bytes it will be refused for.
	if i.upload.size+int64(len(chunk)) > i.limit {
		return i.upload.size, errs.New(errs.KindResourceExhausted,
			"the file is larger than this node accepts").
			WithOp(opAppend).
			WithCode(service.CodeUploadTooLarge).
			WithField("size_bytes", "it must be at most what the node was configured to hold")
	}

	written, err := io.MultiWriter(i.upload.file, i.upload.digest).Write(chunk)

	// Counted before the error is reported, because a short write has still
	// moved the file: a session that reported the old offset would have the
	// caller resend bytes that are already there.
	i.upload.size += int64(written)

	if err != nil {
		return i.upload.size, errs.Wrap(err, errs.KindInternal,
			"the chunk could not be held").
			WithOp(opAppend).
			WithCode(service.CodeStagingFailed)
	}

	return i.upload.size, nil
}

// Received is how many bytes have arrived so far.
func (i *incoming) Received() int64 { return i.upload.size }

// Done stops accepting bytes and hands over what arrived.
func (i *incoming) Done() (service.Staged, error) {
	i.done = true

	return i.upload, nil
}

// Close releases the bytes of an upload that was never finished, and does
// nothing to one that was: the staged file it handed over belongs to whoever
// took it, and closing that here would take the bytes out from under them.
func (i *incoming) Close() error {
	if i.done {
		return nil
	}

	return i.upload.Close()
}

// upload is the staged file: the descriptor, what arrived, and its digest.
type upload struct {
	file   *os.File
	digest hash.Hash
	size   int64
}

// upload satisfies the port's view of a staged upload.
var _ service.Staged = (*upload)(nil)

// Read reads the staged bytes.
func (s *upload) Read(p []byte) (int, error) {
	read, err := s.file.Read(p)

	// io.EOF travels untouched: it is the end of the stream and not a failure,
	// and a caller that wrapped it would break every io.Reader in the chain
	// above this one.
	if err != nil && !errors.Is(err, io.EOF) {
		return read, errs.Wrap(err, errs.KindInternal, "the staged file could not be read").
			WithOp(opRead).
			WithCode(service.CodeStagingFailed)
	}

	return read, err
}

// Rewind returns the reader to the first byte.
func (s *upload) Rewind() error {
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return errs.Wrap(err, errs.KindInternal, "the staged file could not be rewound").
			WithOp(opRewind).
			WithCode(service.CodeStagingFailed)
	}

	return nil
}

// Size is how many bytes arrived.
func (s *upload) Size() int64 { return s.size }

// Digest is the sha-256 of what arrived, as lowercase hexadecimal.
func (s *upload) Digest() string { return hex.EncodeToString(s.digest.Sum(nil)) }

// Close releases the descriptor, which is what releases the bytes: the file
// has had no name since it was created.
func (s *upload) Close() error {
	if err := s.file.Close(); err != nil {
		return errs.Wrap(err, errs.KindInternal, "the staged file could not be released").
			WithOp(opStage).
			WithCode(service.CodeStagingFailed)
	}

	return nil
}
