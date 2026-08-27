package staging_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/infra/service/staging"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// payload is the file that arrives in the uploads below.
const payload = "the bytes of a work"

// digestOf is the lowercase hex sha-256 of s, which is the name the object
// would be stored under.
func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

func TestStageReportsWhatArrived(t *testing.T) {
	t.Parallel()

	staged, err := staging.New().Stage(t.Context(), strings.NewReader(payload), 1024)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	defer func() { _ = staged.Close() }()

	switch {
	case staged.Size() != int64(len(payload)):
		t.Errorf("the staged file is %d bytes, want %d", staged.Size(), len(payload))
	case staged.Digest() != digestOf(payload):
		t.Errorf("the staged file hashes to %q", staged.Digest())
	}
}

// The bytes are read twice: once as they arrive, to find out what they are,
// and once from the beginning, to hand them to the object store.
func TestStagedRewindsForTheStore(t *testing.T) {
	t.Parallel()

	staged, err := staging.New().Stage(t.Context(), strings.NewReader(payload), 1024)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	defer func() { _ = staged.Close() }()

	if err = staged.Rewind(); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	served, err := io.ReadAll(staged)
	if err != nil {
		t.Fatalf("reading the staged file: %v", err)
	}

	if string(served) != payload {
		t.Errorf("the staged file served %q", served)
	}
}

// A client that declared one length and sent another must not be able to fill
// the node by lying about it.
func TestStageRefusesAStreamLongerThanTheCeiling(t *testing.T) {
	t.Parallel()

	_, err := staging.New().Stage(t.Context(), strings.NewReader(payload), 4)

	if !errors.Is(err, errs.KindResourceExhausted) {
		t.Fatalf("Stage = %v, want a resource exhausted", err)
	}

	if code := errs.CodeOf(err); code != service.CodeUploadTooLarge {
		t.Errorf("the refusal is coded %q", code)
	}
}

// A file exactly at the ceiling is accepted, and the first one over it is not.
func TestStageAcceptsAStreamExactlyAtTheCeiling(t *testing.T) {
	t.Parallel()

	staged, err := staging.New().Stage(t.Context(),
		strings.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("a file exactly at the ceiling was refused: %v", err)
	}

	_ = staged.Close()
}

func TestStageReportsAStreamThatDidNotArriveInFull(t *testing.T) {
	t.Parallel()

	_, err := staging.New().Stage(t.Context(), &failingReader{}, 1024)

	if !errors.Is(err, errs.KindUnavailable) {
		t.Errorf("Stage = %v, want the failure to be reported as the transport's", err)
	}
}

// failingReader is a stream that dies partway, which is a mobile network.
type failingReader struct{ served bool }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.served {
		return 0, errors.New("the connection was lost")
	}

	f.served = true

	return copy(p, payload[:3]), nil
}
