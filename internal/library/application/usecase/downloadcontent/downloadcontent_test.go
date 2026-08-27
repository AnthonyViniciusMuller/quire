package downloadcontent_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/application/usecase/downloadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// stored is when the file below arrived, digest is its name, and payload is
// what it holds.
var stored = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

const (
	digest  = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"
	payload = "the bytes of a work"
)

// fixture is the use case over doubles, and the pieces a test asserts against.
type fixture struct {
	usecase  *downloadcontent.DownloadContent
	works    *apptest.EbookRepository
	contents *apptest.ContentRepository
	blobs    *apptest.BlobStore
	reader   uuid.UUID
	phone    uuid.UUID
	work     *ebook.Ebook
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	works := apptest.NewEbookRepository(nil)
	contents := apptest.NewContentRepository()
	blobs := apptest.NewBlobStore()
	reader, phone := uuid.New(), uuid.New()

	work, err := ebook.New(reader, &ebook.Details{Title: "Os Sertões"},
		&ebook.File{Format: ebook.FormatEPUB, Hash: digest, Size: ebook.Size(len(payload))},
		phone, stored)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return &fixture{
		usecase:  downloadcontent.New(works, contents, blobs),
		works:    works,
		contents: contents,
		blobs:    blobs,
		reader:   reader,
		phone:    phone,
		work:     work,
	}
}

// hold puts the bytes in the store and records that this node has them.
func (f *fixture) hold(t *testing.T) {
	t.Helper()

	at, err := f.blobs.Put(t.Context(), &service.Blob{
		Hash: digest, Size: int64(len(payload)), MediaType: "application/epub+zip",
	}, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	record, err := content.New(digest, int64(len(payload)), "application/epub+zip", at, stored)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := f.contents.Create(t.Context(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.hold(t)

	output, err := f.usecase.Execute(t.Context(),
		downloadcontent.Input{UserID: f.reader, EbookID: f.work.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	defer func() { _ = output.Body.Close() }()

	if output.Content.MediaType != "application/epub+zip" {
		t.Errorf("the reply says the bytes are %q", output.Content.MediaType)
	}

	served, err := io.ReadAll(output.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}

	if string(served) != payload {
		t.Errorf("the stream served %q", served)
	}
}

// A node that replicates a reader without their files has every work row and
// none of the bytes, and saying so is what lets a client go and ask the node
// that has them (D02).
func TestExecuteSaysWhenThisNodeDoesNotHoldTheFile(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.usecase.Execute(t.Context(),
		downloadcontent.Input{UserID: f.reader, EbookID: f.work.ID})

	if !errors.Is(err, errs.KindNotFound) {
		t.Fatalf("Execute = %v, want a not found", err)
	}

	if code := errs.CodeOf(err); code != content.CodeNotFound {
		t.Errorf("the reply is coded %q, want %q — a client has to tell this from a work "+
			"that does not exist", code, content.CodeNotFound)
	}
}

// The digest identifies bytes that may be shared with any number of other
// readers, so what decides is the work, which names one.
func TestExecuteRefusesAWorkTheReaderCannotSee(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.hold(t)

	removed, err := f.works.GetByID(t.Context(), f.work.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	removed.Delete(f.phone, stored.Add(time.Hour))

	if err := f.works.Update(t.Context(), removed); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tests := map[string]downloadcontent.Input{
		"somebody else's":  {UserID: uuid.New(), EbookID: f.work.ID},
		"no such work":     {UserID: f.reader, EbookID: uuid.New()},
		"one they deleted": {UserID: f.reader, EbookID: f.work.ID},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := f.usecase.Execute(t.Context(), input)

			if !errors.Is(err, errs.KindNotFound) {
				t.Fatalf("Execute = %v, want a not found", err)
			}

			if code := errs.CodeOf(err); code != ebook.CodeNotFound {
				t.Errorf("the refusal is coded %q, want the work to be what was refused", code)
			}
		})
	}
}

// A row that points at an object the store has lost is an operator's problem
// and not a replication state, and the reply has to be able to say so.
func TestExecuteReportsAStoreThatLostTheObject(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.hold(t)
	f.blobs.OpenErr = errs.New(errs.KindNotFound, "the object store does not have that file").
		WithCode(service.CodeBlobNotFound)

	_, err := f.usecase.Execute(t.Context(),
		downloadcontent.Input{UserID: f.reader, EbookID: f.work.ID})

	if code := errs.CodeOf(err); code != service.CodeBlobNotFound {
		t.Errorf("Execute = %v, coded %q", err, code)
	}
}
