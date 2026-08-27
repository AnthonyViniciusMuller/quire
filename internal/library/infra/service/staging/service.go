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
	file, err := os.CreateTemp("", namePattern)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "the node could not hold the incoming file").
			WithOp(opStage).
			WithCode(service.CodeStagingFailed)
	}

	// Unlinked immediately: the descriptor stays valid, and from here on the
	// bytes have no name anything else could open.
	if err = os.Remove(file.Name()); err != nil {
		_ = file.Close()

		return nil, errs.Wrap(err, errs.KindInternal, "the node could not hold the incoming file").
			WithOp(opStage).
			WithCode(service.CodeStagingFailed)
	}

	staged := &upload{file: file, digest: sha256.New()}

	// One byte past the ceiling, so that a file exactly at it is accepted and
	// the first one over is refused without being read to its end.
	written, err := io.Copy(io.MultiWriter(file, staged.digest), io.LimitReader(body, limit+1))
	if err != nil {
		_ = staged.Close()

		return nil, errs.Wrap(err, errs.KindUnavailable, "the upload did not arrive in full").
			WithOp(opStage).
			WithCode(service.CodeStagingFailed)
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
