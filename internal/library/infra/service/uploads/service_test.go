package uploads_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/library/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/service/uploads"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// The file every test below sends, and the ceiling the node is given.
const (
	book  = "Vidas Secas, in as many pieces as the caller likes."
	limit = 1 << 20
)

// expiry is how long a session may go without a chunk in these tests. It is
// long enough never to be reached by accident; the test about expiry moves the
// clock instead of waiting.
const expiry = time.Hour

// fixture is the registry over the slice's staging double.
type fixture struct {
	uploads *uploads.Service
	reader  uuid.UUID
	ctx     context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	return &fixture{
		uploads: uploads.New(apptest.NewStaging(), expiry, limit, logging.Discard()),
		reader:  uuid.New(),
		ctx:     t.Context(),
	}
}

// declared is what a caller says it is about to send.
func declared(t *testing.T, body string) service.Declared {
	t.Helper()

	sum := sha256.Sum256([]byte(body))

	hash, err := ebook.ParseContentHash(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("parsing the digest: %v", err)
	}

	mediaType, err := content.ParseMediaType("application/epub+zip")
	if err != nil {
		t.Fatalf("parsing the media type: %v", err)
	}

	return service.Declared{Hash: hash, Size: int64(len(body)), MediaType: mediaType}
}

// TestAFileArrivesInPieces is the whole point: the bytes reach the node across
// several calls and produce the staged file a streamed upload would.
func TestAFileArrivesInPieces(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	var offset int64

	for _, chunk := range []string{book[:10], book[10:30], book[30:]} {
		put, appendErr := f.uploads.Append(f.ctx, f.reader, began.ID, offset, []byte(chunk))
		if appendErr != nil {
			t.Fatalf("Append at %d: %v", offset, appendErr)
		}

		offset = put.Received
	}

	finished, err := f.uploads.Finish(f.ctx, f.reader, began.ID)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	defer func() { _ = finished.Staged.Close() }()

	if finished.Staged.Size() != int64(len(book)) {
		t.Errorf("the staged file holds %d bytes, want %d", finished.Staged.Size(), len(book))
	}

	// The digest the node computed, over the bytes the node received. It is
	// what the use case compares against the declaration, and the reason the
	// chunked shape gives away nothing the streamed one guarantees.
	sum := sha256.Sum256([]byte(book))
	if finished.Staged.Digest() != hex.EncodeToString(sum[:]) {
		t.Errorf("the digest is %s, want the digest of what was sent", finished.Staged.Digest())
	}

	read, err := io.ReadAll(finished.Staged)
	if err != nil {
		t.Fatalf("reading the staged file: %v", err)
	}

	if string(read) != book {
		t.Errorf("the staged file holds %q, want the file that was sent", string(read))
	}
}

// TestTheDeclarationIsTheOneFromTheBeginning is what stops a caller declaring a
// small file to pass the ceiling and finishing a large one.
func TestTheDeclarationIsTheOneFromTheBeginning(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	want := declared(t, book)

	began, err := f.uploads.Begin(f.ctx, f.reader, want)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	finished, err := f.uploads.Finish(f.ctx, f.reader, began.ID)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	defer func() { _ = finished.Staged.Close() }()

	if finished.Declared != want {
		t.Errorf("the session finished as %+v, want the declaration it began with %+v",
			finished.Declared, want)
	}
}

// TestAChunkAtTheWrongOffsetIsNotWritten is the resumability the stream this
// replaces does not have.
//
// The chunk is refused without an error, and the answer carries the offset the
// node holds — so a caller whose connection died continues from there rather
// than from the beginning, and one that lost an answer and resent does not
// write the same bytes twice.
func TestAChunkAtTheWrongOffsetIsNotWritten(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	first, err := f.uploads.Append(f.ctx, f.reader, began.ID, 0, []byte(book[:10]))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	// The same chunk again, as a caller that lost the answer above would send.
	repeated, err := f.uploads.Append(f.ctx, f.reader, began.ID, 0, []byte(book[:10]))
	if err != nil {
		t.Fatalf("a repeated chunk was refused with an error: %v", err)
	}

	if repeated.Received != first.Received {
		t.Errorf("a repeated chunk moved the session to %d, want it to stay at %d",
			repeated.Received, first.Received)
	}

	// And a chunk from beyond where the node is, which a caller that skipped
	// ahead would send.
	ahead, err := f.uploads.Append(f.ctx, f.reader, began.ID, 999, []byte(book[10:]))
	if err != nil {
		t.Fatalf("a chunk beyond the offset was refused with an error: %v", err)
	}

	if ahead.Received != first.Received {
		t.Errorf("a chunk at offset 999 moved the session to %d, want it to stay at %d",
			ahead.Received, first.Received)
	}
}

// TestBytesBeyondTheCeilingAreRefused is the bound that survives a client
// lying about the length it declared.
func TestBytesBeyondTheCeilingAreRefused(t *testing.T) {
	t.Parallel()

	small := uploads.New(apptest.NewStaging(), expiry, 8, logging.Discard())
	reader := uuid.New()

	began, err := small.Begin(t.Context(), reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, err = small.Append(t.Context(), reader, began.ID, 0, []byte(book))
	if err == nil {
		t.Fatal("a chunk past the ceiling was accepted")
	}

	if !errors.Is(err, errs.KindResourceExhausted) {
		t.Errorf("the refusal is %v, want %v", errs.KindOf(err), errs.KindResourceExhausted)
	}
}

// TestASessionOfAnotherReaderIsNotHere is the answer that does not tell a
// stranger when they have guessed an identifier.
func TestASessionOfAnotherReaderIsNotHere(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	stranger := uuid.New()

	_, err = f.uploads.Append(f.ctx, stranger, began.ID, 0, []byte(book))
	if err == nil {
		t.Fatal("a stranger wrote to somebody else's upload")
	}

	// Not found, and deliberately not permission denied: the two are
	// distinguishable, and one of them confirms the identifier is real.
	if !errors.Is(err, errs.KindNotFound) {
		t.Errorf("the refusal is %v, want %v", errs.KindOf(err), errs.KindNotFound)
	}
}

// TestAReaderMayNotOpenUnboundedSessions is the other half of the disk bound:
// each session is a descriptor and a file, and a caller that never sends a byte
// should not be able to take the node's disk by opening them.
func TestAReaderMayNotOpenUnboundedSessions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	opened := 0

	for range 64 {
		if _, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book)); err != nil {
			if !errors.Is(err, errs.KindResourceExhausted) {
				t.Fatalf("the refusal is %v, want %v", errs.KindOf(err), errs.KindResourceExhausted)
			}

			break
		}

		opened++
	}

	if opened == 64 {
		t.Fatal("a reader opened sixty-four sessions, so nothing bounds them")
	}

	// And the bound is per reader, not per node: another reader is unaffected
	// by the first having filled theirs.
	if _, err := f.uploads.Begin(f.ctx, uuid.New(), declared(t, book)); err != nil {
		t.Errorf("a second reader was refused a session because the first had filled theirs: %v", err)
	}
}

// TestFinishingReleasesTheReadersPlace covers the bookkeeping behind the bound:
// a session that ended has to stop counting against the reader who opened it.
func TestFinishingReleasesTheReadersPlace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for range 16 {
		began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
		if err != nil {
			t.Fatalf("Begin after finishing the last: %v", err)
		}

		finished, err := f.uploads.Finish(f.ctx, f.reader, began.ID)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}

		_ = finished.Staged.Close()
	}
}

// TestDiscardingReleasesItToo is the same bookkeeping on the path a caller
// takes when it gives up.
func TestDiscardingReleasesItToo(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for range 16 {
		began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
		if err != nil {
			t.Fatalf("Begin after discarding the last: %v", err)
		}

		if err := f.uploads.Discard(f.ctx, f.reader, began.ID); err != nil {
			t.Fatalf("Discard: %v", err)
		}
	}
}

// TestAFinishedSessionIsGone is what makes Finish an ending rather than a read:
// the bytes have been handed to the caller, and a second call must not hand
// them over again.
func TestAFinishedSessionIsGone(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	finished, err := f.uploads.Finish(f.ctx, f.reader, began.ID)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	_ = finished.Staged.Close()

	if _, err := f.uploads.Finish(f.ctx, f.reader, began.ID); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("finishing twice answered %v, want %v", errs.KindOf(err), errs.KindNotFound)
	}
}

// TestTheSweeperEndsWhatNobodyIsSendingTo is the bound a client cannot be
// relied on for: Discard covers the caller that gave up politely, and this
// covers the one whose network went away.
func TestTheSweeperEndsWhatNobodyIsSendingTo(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Past the deadline without waiting for one, which is what the injected
	// clock is for.
	f.uploads.SetClock(func() time.Time { return time.Now().Add(2 * expiry) })
	f.uploads.SweepOnce(f.ctx)

	if _, err := f.uploads.Append(f.ctx, f.reader, began.ID, 0, []byte(book)); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("the session survived the sweep: %v", errs.KindOf(err))
	}
}

// TestTheSweeperLeavesASessionBeingWrittenTo is the case that would otherwise
// pull the bytes out from under a call in flight: a long chunk arriving as the
// deadline passes.
func TestTheSweeperLeavesASessionBeingWrittenTo(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	f.uploads.SetClock(func() time.Time { return time.Now().Add(2 * expiry) })
	f.uploads.SetBusy(began.ID)
	f.uploads.SweepOnce(f.ctx)

	f.uploads.ClearBusy(began.ID)

	if _, err := f.uploads.Append(f.ctx, f.reader, began.ID, 0, []byte(book[:4])); err != nil {
		t.Errorf("a session with a call inside it was swept: %v", err)
	}
}

// TestRunReleasesEverythingWhenTheNodeStops is why the sweeper is in the node's
// group rather than a goroutine nobody waits for.
func TestRunReleasesEverythingWhenTheNodeStops(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	began, err := f.uploads.Begin(f.ctx, f.reader, declared(t, book))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	ctx, stop := context.WithCancel(f.ctx)
	finished := make(chan error, 1)

	go func() { finished <- f.uploads.Run(ctx) }()

	stop()

	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Run returned %v, want the cancellation to be an ordinary stop", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	if _, err := f.uploads.Append(f.ctx, f.reader, began.ID, 0, []byte(book)); !errors.Is(err, errs.KindNotFound) {
		t.Errorf("a session survived the node stopping: %v", errs.KindOf(err))
	}
}
