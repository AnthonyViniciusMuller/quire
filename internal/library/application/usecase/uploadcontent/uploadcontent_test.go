package uploadcontent_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/uploadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// stored is when the uploads below arrived, and payload is the file that
// arrives in them.
var stored = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const (
	payload = "the bytes of a work"
	limit   = int64(1024)
)

// digestOf is the lowercase hex sha-256 of s, which is both the name the
// object is stored under and what the client declares.
func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase  *uploadcontent.UploadContent
	works    *apptest.EbookRepository
	contents *apptest.ContentRepository
	blobs    *apptest.BlobStore
	staging  *apptest.Staging
	reader   uuid.UUID
	phone    uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	works := apptest.NewEbookRepository(nil)
	contents := apptest.NewContentRepository()
	blobs := apptest.NewBlobStore()
	staging := apptest.NewStaging()
	reader, phone := uuid.New(), uuid.New()

	f := &fixture{
		usecase: uploadcontent.New(works, contents, blobs, staging,
			apptest.NewClock(stored), limit),
		works: works, contents: contents, blobs: blobs, staging: staging,
		reader: reader, phone: phone,
	}

	// The flow the contract describes: the work is recorded first, and its
	// reply is what tells the client to upload.
	f.claim(t, f.reader, digestOf(payload))

	return f
}

// claim records a work of owner's naming the digest, which is what C16 checks
// an upload against.
func (f *fixture) claim(t *testing.T, owner uuid.UUID, digest string) {
	t.Helper()

	work, err := ebook.New(owner, &ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: ebook.ContentHash(digest), Size: ebook.Size(len(payload))},
		f.phone, stored)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// input is a well-formed upload of payload.
func (f *fixture) input() uploadcontent.Input {
	return uploadcontent.Input{
		UserID:      f.reader,
		ContentHash: digestOf(payload),
		Size:        int64(len(payload)),
		MediaType:   "application/epub+zip",
		Body:        strings.NewReader(payload),
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.AlreadyHeld:
		t.Error("a node that held nothing reported that it already had the file")
	case output.Content.Hash != ebook.ContentHash(digestOf(payload)):
		t.Error("the record is keyed by something other than the digest of the bytes")
	case output.Content.Size != int64(len(payload)):
		t.Errorf("the record says the file is %d bytes", output.Content.Size)
	case output.Content.Bucket != f.blobs.Bucket():
		t.Error("the record does not say which container the object went into")
	}

	held, found := f.blobs.Stored(output.Content.Hash)
	if !found {
		t.Fatal("the record was written and the bytes were not stored")
	}

	if string(held) != payload {
		t.Errorf("the store holds %q", held)
	}

	if _, err := f.contents.GetByHash(t.Context(), output.Content.Hash); err != nil {
		t.Errorf("the bytes were stored and no row points at them: %v", err)
	}
}

// The digest is the key, so bytes already here are the bytes being sent — and
// the reply comes before the stream is read, so the transfer does not happen.
func TestExecuteAnswersAFileThisNodeAlreadyHasWithoutReadingIt(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	input := f.input()
	body := &countingReader{Reader: strings.NewReader(payload)}
	input.Body = body

	output, err := f.usecase.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !output.AlreadyHeld {
		t.Error("the node asked for a file it already had")
	}

	if body.read != 0 {
		t.Errorf("%d bytes were read from a stream the node did not need", body.read)
	}
}

// Storing bytes under a name that promises they are something else is what the
// staging exists to prevent, and every later reader of that object trusts the
// name.
func TestExecuteRefusesBytesThatAreNotWhatWasDeclared(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := f.input()
	input.Body = strings.NewReader("something else entirely")
	input.Size = int64(len("something else entirely"))

	_, err := f.usecase.Execute(t.Context(), input)
	if errs.CodeOf(err) != uploadcontent.CodeDigestMismatch {
		t.Fatalf("Execute = %v, want a digest mismatch", err)
	}

	if _, found := f.blobs.Stored(ebook.ContentHash(digestOf(payload))); found {
		t.Error("the bytes were stored under the digest they were falsely declared with")
	}
}

// A truncated transfer fails both checks, and telling the client which is the
// difference between a bug it can find and one it cannot.
func TestExecuteRefusesATransferThatWasCutShort(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := f.input()
	input.Body = strings.NewReader(payload[:5])

	_, err := f.usecase.Execute(t.Context(), input)
	if errs.CodeOf(err) != uploadcontent.CodeSizeMismatch {
		t.Errorf("Execute = %v, want a size mismatch", err)
	}
}

// The declared length is refused before the bytes travel, which is the whole
// reason the contract sends the description in its own message.
func TestExecuteRefusesAnOversizedFileBeforeReadingIt(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := f.input()
	body := &countingReader{Reader: strings.NewReader(payload)}
	input.Body = body
	input.Size = limit + 1

	_, err := f.usecase.Execute(t.Context(), input)

	if !errors.Is(err, errs.KindResourceExhausted) {
		t.Fatalf("Execute = %v, want a resource exhausted", err)
	}

	if errs.CodeOf(err) != service.CodeUploadTooLarge {
		t.Errorf("the refusal is coded %q", errs.CodeOf(err))
	}

	if body.read != 0 {
		t.Errorf("%d bytes of an oversized file were read", body.read)
	}
}

// C16: the upload carries no work identifier, so without this check the object
// store is writable by any authenticated reader under any name.
func TestExecuteRefusesADigestNoWorkOfTheCallersNames(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := f.input()
	input.UserID = uuid.New()

	_, err := f.usecase.Execute(t.Context(), input)

	if !errors.Is(err, errs.KindFailedPrecondition) {
		t.Fatalf("Execute = %v, want a failed precondition", err)
	}

	if errs.CodeOf(err) != uploadcontent.CodeUnclaimedContent {
		t.Errorf("the refusal is coded %q", errs.CodeOf(err))
	}

	if _, found := f.blobs.Stored(ebook.ContentHash(digestOf(payload))); found {
		t.Error("a reader with no work naming the digest stored bytes under it")
	}
}

// A work the reader deleted on one device while another was still uploading
// its file is an ordinary crossing, not an attempt to store something they
// have no claim to.
func TestExecuteAcceptsAFileWhoseWorkWasTombstoned(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	works, _, err := f.works.List(t.Context(), &ebook.Query{UserID: f.reader, Size: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	works[0].Delete(f.phone, stored.Add(time.Hour))

	if err := f.works.Update(t.Context(), works[0]); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := f.usecase.Execute(t.Context(), f.input()); err != nil {
		t.Errorf("Execute: %v", err)
	}
}

// The object is stored before the row that points at it, because the other
// order would leave a row promising a file that is not there — which nothing
// repairs and every download believes. What that costs is this: a row that
// cannot be written leaves an object nothing points at, so the upload removes
// it rather than paying a book's worth of disk for a failure.
func TestExecuteRemovesTheObjectWhenTheRowCannotBeWritten(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.contents.CreateErr = errs.New(errs.KindUnavailable, "the database is not answering")

	if _, err := f.usecase.Execute(t.Context(), f.input()); !errors.Is(err, errs.KindUnavailable) {
		t.Fatalf("Execute = %v, want the failure to reach the caller", err)
	}

	if _, found := f.blobs.Stored(ebook.ContentHash(digestOf(payload))); found {
		t.Error("bytes nothing points at were left in the store")
	}

	if removed := f.blobs.Removed(); len(removed) != 1 {
		t.Errorf("the upload removed %d objects, want the one it had just written", len(removed))
	}
}

// A stream that dies partway is a mobile network, and it must leave nothing
// behind either.
func TestExecuteStoresNothingWhenTheStreamDies(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	input := f.input()
	input.Body = &failingReader{}

	if _, err := f.usecase.Execute(t.Context(), input); err == nil {
		t.Fatal("an upload whose stream failed was accepted")
	}

	if _, found := f.blobs.Stored(ebook.ContentHash(digestOf(payload))); found {
		t.Error("bytes from a failed upload were left in the store")
	}
}

func TestExecuteRefusesWhatCannotDescribeAFile(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*uploadcontent.Input){
		"no digest":     func(in *uploadcontent.Input) { in.ContentHash = "" },
		"not a digest":  func(in *uploadcontent.Input) { in.ContentHash = "nope" },
		"no media type": func(in *uploadcontent.Input) { in.MediaType = "" },
		"a media type that could not travel in a header": func(in *uploadcontent.Input) {
			in.MediaType = "application/epub\nx: y"
		},
		"no bytes": func(in *uploadcontent.Input) { in.Size = 0 },
	}

	for name, breaks := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			input := f.input()
			breaks(&input)

			if _, err := f.usecase.Execute(t.Context(), input); !errors.Is(err, errs.KindInvalidArgument) {
				t.Errorf("Execute = %v, want an invalid argument", err)
			}
		})
	}
}

// countingReader records how much of a stream was actually read, which is how
// a test asserts that a transfer did not happen.
type countingReader struct {
	io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	read, err := c.Reader.Read(p)
	c.read += read

	return read, err
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
