package beginupload_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/upload"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/beginupload"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/service/uploads"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// The file the tests declare, and the ceiling the node is given.
const (
	payload = "Vidas Secas, sent a piece at a time."
	limit   = 1 << 20
)

// stored is when the node records what it holds.
var stored = time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)

// fixture is the use case over the slice's doubles and the real session store.
type fixture struct {
	usecase  *beginupload.BeginUpload
	works    *apptest.EbookRepository
	contents *apptest.ContentRepository
	reader   uuid.UUID
	phone    uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	works := apptest.NewEbookRepository(nil)
	contents := apptest.NewContentRepository()
	rules := upload.New(works, contents, apptest.NewBlobStore(), apptest.NewClock(stored), limit)
	sessions := uploads.New(apptest.NewStaging(), time.Hour, limit, logging.Discard())

	return &fixture{
		usecase:  beginupload.New(rules, sessions),
		works:    works,
		contents: contents,
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

// hold records that this node already has the bytes.
func (f *fixture) hold(t *testing.T, digest string) {
	t.Helper()

	mediaType, err := content.ParseMediaType("application/epub+zip")
	if err != nil {
		t.Fatalf("parsing the media type: %v", err)
	}

	record, err := content.New(ebook.ContentHash(digest), int64(len(payload)), mediaType,
		content.Locator{Bucket: "quire-contents", Key: "ebooks/" + digest}, stored)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.contents.Create(t.Context(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// input is a well formed declaration of the payload.
func (f *fixture) input() beginupload.Input {
	return beginupload.Input{
		UserID:      f.reader,
		ContentHash: digestOf(payload),
		Size:        int64(len(payload)),
		MediaType:   "application/epub+zip",
	}
}

// TestBeginningOpensASessionToSendTo is the ordinary path: the node agrees, and
// hands back the identifier the chunks are addressed to.
func TestBeginningOpensASessionToSendTo(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.claim(t, f.reader, digestOf(payload))

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.AlreadyHeld {
		t.Error("the node said it already held a file it has never been sent")
	}

	if output.UploadID == (uuid.UUID{}) {
		t.Error("the node agreed to receive the file and named no upload to send it to")
	}
}

// TestAFileTheNodeAlreadyHoldsOpensNothing is the transfer that does not
// happen.
//
// The digest is the key, so bytes the node holds are the bytes the caller was
// about to send. Opening a session would spend a whole transfer on a file that
// would be discarded at the end of it.
func TestAFileTheNodeAlreadyHoldsOpensNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.claim(t, f.reader, digestOf(payload))
	f.hold(t, digestOf(payload))

	output, err := f.usecase.Execute(t.Context(), f.input())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case !output.AlreadyHeld:
		t.Error("the node opened a session for a file it already holds")
	case output.UploadID != (uuid.UUID{}):
		t.Errorf("the node named upload %s for a file it already holds", output.UploadID)
	case output.Content == nil:
		t.Error("the node said it holds the file and did not say what it holds")
	}
}

// TestADigestNoWorkOfTheCallersNamesIsRefused is C16, checked at the first call
// of the chunked shape exactly as it is checked at the first message of the
// streamed one.
//
// Without it the object store is writable by any authenticated reader, under
// any name, with no row anywhere saying whose file it was.
func TestADigestNoWorkOfTheCallersNamesIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	// Claimed by somebody else, which is the case that would otherwise let a
	// reader with no library write to the store.
	f.claim(t, uuid.New(), digestOf(payload))

	_, err := f.usecase.Execute(t.Context(), f.input())
	if err == nil {
		t.Fatal("a digest no work of the caller's names was admitted")
	}

	if errs.CodeOf(err) != upload.CodeUnclaimedContent {
		t.Errorf("the refusal is %q, want %q", errs.CodeOf(err), upload.CodeUnclaimedContent)
	}
}

// TestAFileLargerThanTheNodeAcceptsIsRefusedBeforeItTravels is the ordering
// this call exists for.
//
// A node that discovered the size by receiving it would have received it, which
// is why the description is a call of its own rather than a field on the first
// chunk.
func TestAFileLargerThanTheNodeAcceptsIsRefusedBeforeItTravels(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.claim(t, f.reader, digestOf(payload))

	input := f.input()
	input.Size = limit + 1

	_, err := f.usecase.Execute(t.Context(), input)
	if err == nil {
		t.Fatal("a file larger than the ceiling was admitted")
	}

	if !errors.Is(err, errs.KindResourceExhausted) {
		t.Errorf("the refusal is %v, want %v", errs.KindOf(err), errs.KindResourceExhausted)
	}
}
