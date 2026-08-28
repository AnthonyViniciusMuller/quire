//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	federationdi "github.com/anthonyvsmuller/quire/internal/federation/di"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	librarydi "github.com/anthonyvsmuller/quire/internal/library/di"
	ebookrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/ebook"
	readingdi "github.com/anthonyvsmuller/quire/internal/reading/di"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/locator"
	"github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	annotationrepository "github.com/anthonyvsmuller/quire/internal/reading/infra/repository/annotation"
	progressrepository "github.com/anthonyvsmuller/quire/internal/reading/infra/repository/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// thePassage is where the marks in these tests are attached.
const thePassage = "epubcfi(/6/14[chap05ref]!/4[body01]/10[para05]/3:10)"

// reading is the node's whole gRPC surface, with a reader registered and signed
// in, and one work already in their collection.
type reading struct {
	library
	client quirev1.ReadingServiceClient
	// ebookID is the work every mark and every position below is in. The
	// reading service establishes whose a row is through the work, so a suite
	// with no work has nothing to test.
	ebookID string
}

// serveReading starts the node with all four slices registered and returns the
// reading client, authenticated as a reader this node hosts.
//
// It builds the real containers rather than assembling use cases by hand, so
// that what these tests exercise is what cmd/quired runs — including the wiring
// this slice needs and no other does: the reading container is handed the
// library's works repository, which is how it answers whether a reader may
// write in a work at all.
func serveReading(t *testing.T) reading {
	t.Helper()
	reset(t)
	resetStorage(t)

	cfg := nodeConfig(t)

	identityContainer, err := identitydi.Initialize(cfg, pool, federationdi.Catalogue(pool), logging.Discard())
	if err != nil {
		t.Fatalf("building the identity slice: %v", err)
	}

	libraryContainer, err := librarydi.Initialize(t.Context(), cfg, pool, hlc.New())
	if err != nil {
		t.Fatalf("building the library slice: %v", err)
	}

	t.Cleanup(func() { _ = libraryContainer.Close() })

	readingContainer := readingdi.Initialize(pool, libraryContainer.Ebooks, hlc.New())

	grpcServer, err := grpcx.New(t.Context(), &cfg.Server,
		grpcx.WithChain(grpcx.NewChain(logging.Discard())),
		grpcx.WithUnaryInterceptors(identityContainer.Interceptor.Unary()),
		grpcx.WithStreamInterceptors(identityContainer.Interceptor.Stream()),
	)
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}

	identityContainer.Service.Register(grpcServer.Registrar())
	libraryContainer.Service.Register(grpcServer.Registrar())
	readingContainer.Service.Register(grpcServer.Registrar())

	ctx, cancel := context.WithCancel(t.Context())

	served := make(chan error, 1)
	go func() { served <- grpcServer.Serve(ctx) }()

	connection, err := grpc.NewClient(grpcServer.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing the node: %v", err)
	}

	t.Cleanup(func() {
		_ = connection.Close()

		cancel()

		if err := <-served; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})

	token, device := signInWithDevice(t, quirev1.NewAuthServiceClient(connection))

	node := reading{
		library: library{
			client:   quirev1.NewLibraryServiceClient(connection),
			ctx:      bearer(t.Context(), token),
			deviceID: device,
		},
		client: quirev1.NewReadingServiceClient(connection),
	}

	node.ebookID = node.createEbook(t, "Os Sertões", theFile).GetId()

	return node
}

// mark records one highlight at place and returns it.
func (r *reading) mark(t *testing.T, place string) *quirev1.Annotation {
	t.Helper()

	created, err := r.client.CreateAnnotation(r.ctx, &quirev1.CreateAnnotationRequest{
		Annotation: &quirev1.Annotation{
			EbookId: r.ebookID,
			Kind:    quirev1.AnnotationKind_ANNOTATION_KIND_HIGHLIGHT,
			Locator: place,
		},
	})
	if err != nil {
		t.Fatalf("CreateAnnotation: %v", err)
	}

	return created.GetAnnotation()
}

// TestReadingRoundTrip walks UC04 and UC05 in the order a reader walks them,
// against a real database and over a real connection.
//
// The subtests share state on purpose and must run in order: what makes this a
// round trip rather than a set of unit tests is that each step starts from what
// the previous one left in the database.
func TestReadingRoundTrip(t *testing.T) {
	node := serveReading(t)

	var note *quirev1.Annotation

	t.Run("the reader writes a note in the work", func(t *testing.T) {
		created, err := node.client.CreateAnnotation(node.ctx, &quirev1.CreateAnnotationRequest{
			Annotation: &quirev1.Annotation{
				EbookId: node.ebookID,
				Kind:    quirev1.AnnotationKind_ANNOTATION_KIND_NOTE,
				Text:    stringPtr("o sertanejo é, antes de tudo, um forte"),
				Locator: thePassage,
			},
		})
		if err != nil {
			t.Fatalf("CreateAnnotation: %v", err)
		}

		note = created.GetAnnotation()

		switch {
		case note.GetId() == "":
			t.Error("the mark was recorded without an identifier the client could hold")
		case note.GetEbookId() != node.ebookID:
			t.Error("the mark does not name the work it was made in")
		case note.GetRevision().GetDeviceId() != node.deviceID:
			t.Error("the revision does not name the device the session belongs to")
		case note.GetRevision().GetVectorClock().GetEntries()[node.deviceID] != 1:
			t.Error("the causal history does not count the mark as an event of that device")
		}
	})

	t.Run("and reads it back", func(t *testing.T) {
		got, err := node.client.GetAnnotation(node.ctx,
			&quirev1.GetAnnotationRequest{AnnotationId: note.GetId()})
		if err != nil {
			t.Fatalf("GetAnnotation: %v", err)
		}

		if got.GetAnnotation().GetText() != note.GetText() {
			t.Errorf("the mark reads %q", got.GetAnnotation().GetText())
		}
	})

	t.Run("editing it claims only the field the mask names", func(t *testing.T) {
		updated, err := node.client.UpdateAnnotation(node.ctx, &quirev1.UpdateAnnotationRequest{
			AnnotationId: note.GetId(),
			Annotation:   &quirev1.Annotation{Text: stringPtr("corrigida")},
			UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"text"}},
		})
		if err != nil {
			t.Fatalf("UpdateAnnotation: %v", err)
		}

		mark := updated.GetAnnotation()

		switch {
		case mark.GetText() != "corrigida":
			t.Errorf("the mark reads %q", mark.GetText())
		case mark.GetLocator() != thePassage:
			t.Error("a field the mask did not name was written")
		case mark.GetKind() != quirev1.AnnotationKind_ANNOTATION_KIND_NOTE:
			t.Error("the kind was rewritten by a mask that did not name it")
		case mark.GetRevision().GetVectorClock().GetEntries()[node.deviceID] != 2:
			t.Error("the edit was not counted as a second event of the writing device")
		}

		note = mark
	})

	t.Run("a mask naming a path the call cannot write is refused", func(t *testing.T) {
		_, err := node.client.UpdateAnnotation(node.ctx, &quirev1.UpdateAnnotationRequest{
			AnnotationId: note.GetId(),
			Annotation:   &quirev1.Annotation{Text: stringPtr("x")},
			UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"ebook_id"}},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("UpdateAnnotation with an unwritable path = %v, want an invalid argument", err)
		}

		// An ignored path is a change the client believes it made, and on a
		// per-field last-writer-wins entity a change nobody made is a change
		// that stays unmade until somebody looks.
		if reason := reasonOf(err); reason != "invalid_field_mask" {
			t.Errorf("the refusal is coded %q, so a client could not tell it from any other", reason)
		}
	})

	t.Run("emptying the text of a note is refused, changing its kind is not", func(t *testing.T) {
		_, err := node.client.UpdateAnnotation(node.ctx, &quirev1.UpdateAnnotationRequest{
			AnnotationId: note.GetId(),
			Annotation:   &quirev1.Annotation{Text: stringPtr("   ")},
			UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"text"}},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("emptying a note = %v, want an invalid argument", err)
		}

		// The same claim with the kind alongside it is a reader turning their
		// note into a highlight, which is what they were asking for.
		updated, err := node.client.UpdateAnnotation(node.ctx, &quirev1.UpdateAnnotationRequest{
			AnnotationId: note.GetId(),
			Annotation: &quirev1.Annotation{
				Kind: quirev1.AnnotationKind_ANNOTATION_KIND_HIGHLIGHT,
				Text: stringPtr(""),
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"kind", "text"}},
		})
		if err != nil {
			t.Fatalf("UpdateAnnotation: %v", err)
		}

		if updated.GetAnnotation().Text != nil {
			t.Error("a highlight with no comment came back carrying the empty string")
		}

		note = updated.GetAnnotation()
	})

	t.Run("the reader records where they stopped", func(t *testing.T) {
		percent := 40.126

		updated, err := node.client.UpdateReadingProgress(node.ctx,
			&quirev1.UpdateReadingProgressRequest{
				EbookId: node.ebookID,
				Locator: "page=42",
				Percent: &percent,
			})
		if err != nil {
			t.Fatalf("UpdateReadingProgress: %v", err)
		}

		position := updated.GetProgress()

		switch {
		case position.GetDeviceId() != node.deviceID:
			t.Error("the position does not belong to the device the session belongs to")
		// numeric(5, 2) keeps two decimal places, so a value the database would
		// round is rounded before it is stored — or what the client is told it
		// stored is not what a later read returns.
		case position.GetPercent() != 40.13:
			t.Errorf("the proportion came back as %v, want 40.13", position.GetPercent())
		case position.GetVectorClock().GetEntries()[node.deviceID] != 1:
			t.Error("the version does not count the write as an event of the reading device")
		}
	})

	t.Run("and moving on overwrites the same row", func(t *testing.T) {
		updated, err := node.client.UpdateReadingProgress(node.ctx,
			&quirev1.UpdateReadingProgressRequest{EbookId: node.ebookID, Locator: "page=99"})
		if err != nil {
			t.Fatalf("UpdateReadingProgress: %v", err)
		}

		if updated.GetProgress().Percent != nil {
			t.Error("a client that computed no proportion had the previous one kept for it")
		}

		if updated.GetProgress().GetVectorClock().GetEntries()[node.deviceID] != 2 {
			t.Error("the move was not counted as a second event of the device")
		}

		listed, err := node.client.ListReadingProgress(node.ctx,
			&quirev1.ListReadingProgressRequest{EbookId: node.ebookID})
		if err != nil {
			t.Fatalf("ListReadingProgress: %v", err)
		}

		// C05, the constraint Quadro 21 does not have: without it the rows
		// accumulate and "where this device stopped in this book" stops having
		// one answer.
		if len(listed.GetProgress()) != 1 {
			t.Errorf("the work holds %d positions for one device", len(listed.GetProgress()))
		}
	})

	t.Run("deleting the mark tombstones it rather than removing it", func(t *testing.T) {
		if _, err := node.client.DeleteAnnotation(node.ctx,
			&quirev1.DeleteAnnotationRequest{AnnotationId: note.GetId()}); err != nil {
			t.Fatalf("DeleteAnnotation: %v", err)
		}

		var (
			deleted bool
			text    *string
		)

		if err := pool.QueryRow(t.Context(),
			"SELECT deleted, text FROM reading.annotations WHERE id = $1",
			uuid.MustParse(note.GetId())).Scan(&deleted, &text); err != nil {
			t.Fatalf("the row was removed rather than tombstoned: %v", err)
		}

		if !deleted {
			t.Error("the row is still present, so a peer would never hear about the deletion")
		}

		listed, err := node.client.ListAnnotations(node.ctx,
			&quirev1.ListAnnotationsRequest{EbookId: node.ebookID})
		if err != nil {
			t.Fatalf("ListAnnotations: %v", err)
		}

		if len(listed.GetAnnotations()) != 0 {
			t.Error("a tombstoned mark is still listed")
		}

		// A second deletion has nothing to tell the reader that the first did
		// not, and stamping it would claim a write that was not made.
		_, err = node.client.DeleteAnnotation(node.ctx,
			&quirev1.DeleteAnnotationRequest{AnnotationId: note.GetId()})
		if status.Code(err) != codes.NotFound {
			t.Errorf("a second deletion = %v, want a not found", err)
		}
	})
}

// TestListAnnotationsPaginatesByKeyset walks every page of a work's marks and
// checks that each is returned exactly once, including while the list is being
// written to.
//
// That last part is the whole reason the ordering is the identifier. Quadro 22
// gives an annotation no creation instant and updated_at is rewritten by every
// edit, so a page ordered by either would move a mark across pages the moment a
// second device edited it — and a client walking the pages would see it twice
// or not at all.
func TestListAnnotationsPaginatesByKeyset(t *testing.T) {
	node := serveReading(t)

	written := make(map[string]bool, 7)

	for index := range 7 {
		written[node.mark(t, "page="+string(rune('a'+index))).GetId()] = false
	}

	var (
		token  string
		edited bool
	)

	for pages := 0; ; pages++ {
		if pages > len(written) {
			t.Fatal("the walk did not terminate, so a cursor repeated a page")
		}

		page, err := node.client.ListAnnotations(node.ctx, &quirev1.ListAnnotationsRequest{
			EbookId:   node.ebookID,
			PageSize:  2,
			PageToken: token,
		})
		if err != nil {
			t.Fatalf("ListAnnotations: %v", err)
		}

		for _, mark := range page.GetAnnotations() {
			if seen, known := written[mark.GetId()]; !known || seen {
				t.Errorf("the walk returned %s twice, or returned one it was not given", mark.GetId())
			}

			written[mark.GetId()] = true
		}

		// After the first page, edit a mark the walk has already returned. Its
		// updated_at moves to the end of any ordering that used it; the
		// identifier does not move, so the walk is unaffected.
		if !edited && len(page.GetAnnotations()) > 0 {
			edited = true

			if _, err = node.client.UpdateAnnotation(node.ctx, &quirev1.UpdateAnnotationRequest{
				AnnotationId: page.GetAnnotations()[0].GetId(),
				Annotation:   &quirev1.Annotation{Locator: "page=zzz"},
				UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"locator"}},
			}); err != nil {
				t.Fatalf("UpdateAnnotation: %v", err)
			}
		}

		if page.GetNextPageToken() == "" {
			break
		}

		token = page.GetNextPageToken()
	}

	for id, seen := range written {
		if !seen {
			t.Errorf("the walk never returned %s", id)
		}
	}
}

// TestReadingIsScopedToTheWorksReader is the check that has no repository
// behind it: reading.annotations and reading.progress reference a work and
// never a reader, so whose a row is is a fact about the work — and the Works
// port is the only thing that establishes it.
func TestReadingIsScopedToTheWorksReader(t *testing.T) {
	node := serveReading(t)

	somebodyElse := uuid.New().String()

	// A work in nobody's collection is answered exactly as one belonging to
	// another reader, which is exactly how one that does not exist is
	// answered. A reply that distinguished them would be an oracle for which
	// identifiers exist and whose they are.
	tests := map[string]func() error{
		"listing the marks in it": func() error {
			_, err := node.client.ListAnnotations(node.ctx,
				&quirev1.ListAnnotationsRequest{EbookId: somebodyElse})

			return err
		},
		"writing a mark in it": func() error {
			_, err := node.client.CreateAnnotation(node.ctx, &quirev1.CreateAnnotationRequest{
				Annotation: &quirev1.Annotation{
					EbookId: somebodyElse,
					Kind:    quirev1.AnnotationKind_ANNOTATION_KIND_BOOKMARK,
					Locator: "page=1",
				},
			})

			return err
		},
		"recording a position in it": func() error {
			_, err := node.client.UpdateReadingProgress(node.ctx,
				&quirev1.UpdateReadingProgressRequest{EbookId: somebodyElse, Locator: "page=1"})

			return err
		},
		"listing the positions in it": func() error {
			_, err := node.client.ListReadingProgress(node.ctx,
				&quirev1.ListReadingProgressRequest{EbookId: somebodyElse})

			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			err := call()
			if status.Code(err) != codes.NotFound {
				t.Fatalf("%s = %v, want a not found", name, err)
			}

			if reason := reasonOf(err); reason != "ebook_not_found" {
				t.Errorf("the refusal is coded %q, so a client could tell this service's answer "+
					"apart from the library's for the same work", reason)
			}
		})
	}

	// The work this reader does own is refused when the mark in it is
	// somebody's else — here, when there is no such mark at all, which is the
	// same answer.
	_, err := node.client.GetAnnotation(node.ctx,
		&quirev1.GetAnnotationRequest{AnnotationId: uuid.New().String()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetAnnotation of a mark nobody has = %v, want a not found", err)
	}

	if reason := reasonOf(err); reason != annotation.CodeNotFound {
		t.Errorf("the refusal is coded %q, want the mark's own code", reason)
	}
}

// TestTheReadingStatements exercises the two repositories against the real
// columns, which is what the doubles in
// internal/reading/application/apptest imitate.
func TestTheReadingStatements(t *testing.T) {
	reset(t)

	manager := persist.NewManager(pool)
	works := ebookrepository.New(manager)
	marks := annotationrepository.New(manager)
	positions := progressrepository.New(manager)

	reader, device := seedReader(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	work := seedEbook(t, works, reader, device, at, "a work to write in")

	t.Run("a mark round trips through every column", func(t *testing.T) {
		mark, err := annotation.New(work.ID, &annotation.Mark{
			Kind:    annotation.KindNote,
			Text:    "o sertanejo é, antes de tudo, um forte",
			Locator: locator.Locator(thePassage),
		}, device, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = marks.Create(t.Context(), mark); err != nil {
			t.Fatalf("Create: %v", err)
		}

		stored, err := marks.GetByID(t.Context(), mark.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}

		switch {
		case stored.Kind != annotation.KindNote || stored.Text != mark.Text:
			t.Errorf("the mark came back as %+v", stored.Mark)
		case stored.Locator != mark.Locator:
			t.Errorf("the passage came back as %q", stored.Locator)
		case !stored.Revision.UpdatedAt.Equal(mark.Revision.UpdatedAt):
			t.Errorf("the tie-break timestamp came back as %s, want the %s that was written — "+
				"a value the column cannot hold would decide conflicts differently in memory "+
				"and on disk", stored.Revision.UpdatedAt, mark.Revision.UpdatedAt)
		case !stored.Revision.VectorClock.Equal(mark.Revision.VectorClock):
			t.Error("the causal history did not survive the jsonb column")
		case stored.Revision.DeviceID != device:
			t.Error("the revision lost the device whose write the row reflects")
		}
	})

	t.Run("a position round trips, proportion and all", func(t *testing.T) {
		percent, err := progress.NewPercent(40.13)
		if err != nil {
			t.Fatalf("NewPercent: %v", err)
		}

		position, err := progress.New(work.ID, device,
			&progress.Position{Locator: locator.Locator("page=42"), Percent: percent}, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = positions.Create(t.Context(), position); err != nil {
			t.Fatalf("Create: %v", err)
		}

		stored, err := positions.GetByPair(t.Context(), work.ID, device)
		if err != nil {
			t.Fatalf("GetByPair: %v", err)
		}

		switch {
		case stored.Locator != position.Locator:
			t.Errorf("the position came back as %q", stored.Locator)
		// numeric(5, 2) is an exact decimal and the column is read back through
		// a float64, which is the one override this slice's sqlc block carries.
		case !stored.Percent.IsKnown() || stored.Percent.Float64() != 40.13:
			t.Errorf("the proportion came back as %+v", stored.Percent)
		case !stored.Version.UpdatedAt.Equal(position.Version.UpdatedAt):
			t.Errorf("the version timestamp came back as %s, want %s",
				stored.Version.UpdatedAt, position.Version.UpdatedAt)
		case !stored.Version.VectorClock.Equal(position.Version.VectorClock):
			t.Error("the causal history did not survive the jsonb column")
		}
	})

	// C05, the constraint Quadro 21 does not have. Without it the rows
	// accumulate instead of being updated.
	t.Run("the pair of a position is unique", func(t *testing.T) {
		second := seedEbook(t, works, reader, device, at, "another work")

		first, err := progress.New(second.ID, device,
			&progress.Position{Locator: "page=1", Percent: progress.NoPercent()}, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = positions.Create(t.Context(), first); err != nil {
			t.Fatalf("Create: %v", err)
		}

		again, err := progress.New(second.ID, device,
			&progress.Position{Locator: "page=2", Percent: progress.NoPercent()}, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		err = positions.Create(t.Context(), again)
		if !errors.Is(err, errs.KindAlreadyExists) {
			t.Fatalf("Create of a second position for the same pair = %v, want an already exists", err)
		}

		if code := errs.CodeOf(err); code != progress.CodeAlreadyExists {
			t.Errorf("the refusal is coded %q, so a caller could not tell it from any other "+
				"write failure and would fail instead of moving the row", code)
		}
	})

	// The clearing of a proportion has to reach the column, or a device that
	// stops computing one keeps reporting the last it computed.
	t.Run("a move that says nothing about the proportion clears it", func(t *testing.T) {
		third := seedEbook(t, works, reader, device, at, "a third work")

		position, err := progress.New(third.ID, device,
			&progress.Position{Locator: "page=1", Percent: mustPercent(t, 10)}, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = positions.Create(t.Context(), position); err != nil {
			t.Fatalf("Create: %v", err)
		}

		if err = position.MoveTo(&progress.Position{
			Locator: "page=2",
			Percent: progress.NoPercent(),
		}, at.Add(time.Minute)); err != nil {
			t.Fatalf("MoveTo: %v", err)
		}

		if err = positions.Update(t.Context(), position); err != nil {
			t.Fatalf("Update: %v", err)
		}

		stored, err := positions.GetByPair(t.Context(), third.ID, device)
		if err != nil {
			t.Fatalf("GetByPair: %v", err)
		}

		if stored.Percent.IsKnown() {
			t.Errorf("the proportion came back as %v, want the NULL the column holds",
				stored.Percent.Float64())
		}
	})

	// The column is ON DELETE SET NULL, so a mark can lose the device it
	// names. The revision then has one half of its tie-break instead of two,
	// which is the right outcome — a record whose author no longer exists
	// cannot be attributed to them, and the timestamp still orders it.
	t.Run("a mark survives losing the device that wrote it", func(t *testing.T) {
		mark, err := annotation.New(work.ID, &annotation.Mark{
			Kind:    annotation.KindBookmark,
			Locator: locator.Locator("page=7"),
		}, device, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = marks.Create(t.Context(), mark); err != nil {
			t.Fatalf("Create: %v", err)
		}

		if _, err = pool.Exec(t.Context(),
			"DELETE FROM identity.devices WHERE id = $1", device); err != nil {
			t.Fatalf("deleting the device: %v", err)
		}

		stored, err := marks.GetByID(t.Context(), mark.ID)
		if err != nil {
			t.Fatalf("the mark went with the device: %v", err)
		}

		if stored.Revision.DeviceID != (uuid.UUID{}) {
			t.Error("a NULL device came back as one")
		}

		if !stored.Revision.VectorClock.Equal(mark.Revision.VectorClock) {
			t.Error("the causal history was lost with the device, so the mark could not be reconciled")
		}
	})
}

// TestTwoWritesFromOneDeviceReachOneRow is the pair constraint and the retry,
// checked by making two calls from the same device contend.
//
// There is no row to lock the first time through, so nothing serializes the two
// reads: both see no position and both try to insert one. The constraint
// refuses the loser and the use case answers it by reading the row the winner
// wrote and moving that — which is why a device retrying after a lost reply
// does not get an error it can do nothing about.
func TestTwoWritesFromOneDeviceReachOneRow(t *testing.T) {
	node := serveReading(t)

	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		results [2]error
	)

	start.Add(1)
	done.Add(2)

	for index := range results {
		go func() {
			defer done.Done()

			start.Wait()

			_, results[index] = node.client.UpdateReadingProgress(node.ctx,
				&quirev1.UpdateReadingProgressRequest{
					EbookId: node.ebookID,
					Locator: "page=" + string(rune('a'+index)),
				})
		}()
	}

	start.Done()
	done.Wait()

	for index, err := range results {
		if err != nil {
			t.Errorf("the %s call = %v, want both to succeed", []string{"first", "second"}[index], err)
		}
	}

	listed, err := node.client.ListReadingProgress(node.ctx,
		&quirev1.ListReadingProgressRequest{EbookId: node.ebookID})
	if err != nil {
		t.Fatalf("ListReadingProgress: %v", err)
	}

	if len(listed.GetProgress()) != 1 {
		t.Fatalf("the two calls left %d rows, want the one the pair admits", len(listed.GetProgress()))
	}

	// Both writes are events of the same device, and its counter is what makes
	// them totally ordered: the second built on the first rather than replacing
	// its history (C05).
	if counter := listed.GetProgress()[0].GetVectorClock().GetEntries()[node.deviceID]; counter != 2 {
		t.Errorf("the surviving row counts %d events, want the two that were made", counter)
	}
}

// mustPercent is a proportion a test knows is in range.
func mustPercent(t *testing.T, value float64) progress.Percent {
	t.Helper()

	percent, err := progress.NewPercent(value)
	if err != nil {
		t.Fatalf("NewPercent: %v", err)
	}

	return percent
}
