package finishupload_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/application/upload"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/beginupload"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/finishupload"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/putchunk"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/uploadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/service/uploads"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// The file the tests send, and the ceiling the node is given.
const (
	payload = "Vidas Secas, sent a piece at a time."
	limit   = 1 << 20
)

// stored is when the node records what it holds.
var stored = time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)

// fixture is the three chunked use cases and the streamed one, over one set of
// doubles — which is what lets a test send the same file both ways and compare.
type fixture struct {
	begin    *beginupload.BeginUpload
	put      *putchunk.PutChunk
	finish   *finishupload.FinishUpload
	streamed *uploadcontent.UploadContent

	works    *apptest.EbookRepository
	contents *apptest.ContentRepository
	blobs    *apptest.BlobStore
	reader   uuid.UUID
	phone    uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	works := apptest.NewEbookRepository(nil)
	contents := apptest.NewContentRepository()
	blobs := apptest.NewBlobStore()
	staging := apptest.NewStaging()
	rules := upload.New(works, contents, blobs, apptest.NewClock(stored), limit)
	sessions := uploads.New(staging, time.Hour, limit, logging.Discard())

	return &fixture{
		begin:    beginupload.New(rules, sessions),
		put:      putchunk.New(sessions),
		finish:   finishupload.New(rules, sessions),
		streamed: uploadcontent.New(rules, staging),
		works:    works,
		contents: contents,
		blobs:    blobs,
		reader:   uuid.New(),
		phone:    uuid.New(),
	}
}

// digestOf is the digest a caller declares for a body.
func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))

	return hex.EncodeToString(sum[:])
}

// claim records the work that names the digest, which is the flow the contract
// describes and the precondition C16 requires.
func (f *fixture) claim(t *testing.T, owner uuid.UUID, digest string, size int) {
	t.Helper()

	work, err := ebook.New(owner, &ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: ebook.ContentHash(digest), Size: ebook.Size(size)},
		f.phone, stored)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// send runs the whole chunked flow for a body, in pieces of the given size,
// and returns what the last call answered.
func (f *fixture) send(t *testing.T, body string, piece int) (finishupload.Output, error) {
	t.Helper()

	began, err := f.begin.Execute(t.Context(), beginupload.Input{
		UserID:      f.reader,
		ContentHash: digestOf(body),
		Size:        int64(len(body)),
		MediaType:   "application/epub+zip",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	var offset int64

	for start := 0; start < len(body); start += piece {
		end := min(start+piece, len(body))

		put, putErr := f.put.Execute(t.Context(), putchunk.Input{
			UserID:   f.reader,
			UploadID: began.UploadID,
			Offset:   offset,
			Chunk:    []byte(body[start:end]),
		})
		if putErr != nil {
			t.Fatalf("Put at %d: %v", offset, putErr)
		}

		offset = put.ReceivedBytes
	}

	return f.finish.Execute(t.Context(), finishupload.Input{
		UserID: f.reader, UploadID: began.UploadID,
	})
}

// TestAFileSentInPiecesIsStored is the round trip these three calls exist for.
func TestAFileSentInPiecesIsStored(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.claim(t, f.reader, digestOf(payload), len(payload))

	output, err := f.send(t, payload, 8)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if output.Content == nil {
		t.Fatal("the upload finished and the node recorded nothing")
	}

	if output.Content.Hash != ebook.ContentHash(digestOf(payload)) {
		t.Errorf("the record names %s, want the digest of what was sent", output.Content.Hash)
	}

	held, stored := f.blobs.Stored(output.Content.Hash)
	if !stored {
		t.Fatal("the record was written and the object store holds nothing")
	}

	if string(held) != payload {
		t.Errorf("the object store holds %q, want the file that was sent", string(held))
	}
}

// TestBothShapesStoreTheSameThing is the claim D11 makes, checked rather than
// asserted: the two shapes of UC02 differ in how the bytes arrive and in
// nothing else.
//
// The same file is sent one way and then the other, against the same node, and
// the second is told the node already holds it — which can only happen if the
// first stored it under the digest the second declared.
func TestBothShapesStoreTheSameThing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.claim(t, f.reader, digestOf(payload), len(payload))

	chunked, err := f.send(t, payload, 8)
	if err != nil {
		t.Fatalf("the chunked shape: %v", err)
	}

	streamed, err := f.streamed.Execute(t.Context(), uploadcontent.Input{
		UserID:      f.reader,
		ContentHash: digestOf(payload),
		Size:        int64(len(payload)),
		MediaType:   "application/epub+zip",
		Body:        strings.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("the streamed shape: %v", err)
	}

	if !streamed.AlreadyHeld {
		t.Error("the streamed shape did not recognize the file the chunked shape stored")
	}

	if streamed.Content.Hash != chunked.Content.Hash {
		t.Errorf("the two shapes stored %s and %s", chunked.Content.Hash, streamed.Content.Hash)
	}

	chunkedBytes, _ := f.blobs.Stored(chunked.Content.Hash)
	if string(chunkedBytes) != payload {
		t.Errorf("the object store holds %q for the chunked shape", string(chunkedBytes))
	}
}

// TestBytesThatAreNotWhatWasDeclaredAreRefused is the digest check, which the
// chunked shape makes in the same place and against the same kind of staged
// file as the streamed one.
//
// The node hashes what it received, never what it was told, so a caller that
// declared one file and sent another is refused at the end — and nothing is
// stored under a name that lies about it.
func TestBytesThatAreNotWhatWasDeclaredAreRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	// The work names the digest of the payload, and the caller sends something
	// else under it.
	f.claim(t, f.reader, digestOf(payload), len(payload))

	began, err := f.begin.Execute(t.Context(), beginupload.Input{
		UserID:      f.reader,
		ContentHash: digestOf(payload),
		Size:        int64(len(payload)),
		MediaType:   "application/epub+zip",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	impostor := strings.Repeat("x", len(payload))

	if _, err = f.put.Execute(t.Context(), putchunk.Input{
		UserID: f.reader, UploadID: began.UploadID, Offset: 0, Chunk: []byte(impostor),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, err = f.finish.Execute(t.Context(), finishupload.Input{
		UserID: f.reader, UploadID: began.UploadID,
	})
	if err == nil {
		t.Fatal("bytes that are not the declared file were stored")
	}

	if errs.CodeOf(err) != upload.CodeDigestMismatch {
		t.Errorf("the refusal is %q, want %q", errs.CodeOf(err), upload.CodeDigestMismatch)
	}
}

// TestATruncatedUploadIsRefused is the length check, which is the one that
// explains: a transfer cut short fails the digest too, and saying which makes
// the difference between a bug a client can find and one it cannot.
func TestATruncatedUploadIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.claim(t, f.reader, digestOf(payload), len(payload))

	began, err := f.begin.Execute(t.Context(), beginupload.Input{
		UserID:      f.reader,
		ContentHash: digestOf(payload),
		Size:        int64(len(payload)),
		MediaType:   "application/epub+zip",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if _, err = f.put.Execute(t.Context(), putchunk.Input{
		UserID: f.reader, UploadID: began.UploadID, Offset: 0, Chunk: []byte(payload[:10]),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, err = f.finish.Execute(t.Context(), finishupload.Input{
		UserID: f.reader, UploadID: began.UploadID,
	})
	if err == nil {
		t.Fatal("a truncated upload was stored")
	}

	if errs.CodeOf(err) != upload.CodeSizeMismatch {
		t.Errorf("the refusal is %q, want %q", errs.CodeOf(err), upload.CodeSizeMismatch)
	}
}

// TestFinishingAnUploadThatIsNotHereIsRefused covers the caller that finished
// twice, or whose session the node ended while it was away.
func TestFinishingAnUploadThatIsNotHereIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.finish.Execute(t.Context(), finishupload.Input{
		UserID: f.reader, UploadID: uuid.New(),
	})
	if err == nil {
		t.Fatal("an upload the node is not holding was finished")
	}

	if errs.CodeOf(err) != service.CodeNoSuchUpload {
		t.Errorf("the refusal is %q, want %q", errs.CodeOf(err), service.CodeNoSuchUpload)
	}
}
