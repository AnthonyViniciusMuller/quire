//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	federationdi "github.com/anthonyvsmuller/quire/internal/federation/di"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	addtocollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/addtocollection"
	deletecollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deletecollection"
	librarydi "github.com/anthonyvsmuller/quire/internal/library/di"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	collectionrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/collection"
	ebookrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/ebook"
	membershiprepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/membership"
	clockservice "github.com/anthonyvsmuller/quire/internal/library/infra/service/clock"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// theFile is what the reader of these tests imports, and theDigest is its name
// in the object store. Computing the digest rather than writing it down is
// what keeps the two from drifting when the bytes change.
const theFile = "the bytes of Os Sertões, such as they are"

// theDigest is theFile's sha-256, as lowercase hexadecimal.
func theDigest() string { return digestOf(theFile) }

// digestOf is the name a file is stored under.
func digestOf(payload string) string {
	sum := sha256.Sum256([]byte(payload))

	return hex.EncodeToString(sum[:])
}

// library is the node's whole gRPC surface, with a reader registered and
// signed in.
type library struct {
	client quirev1.LibraryServiceClient
	// ctx already carries the reader's access token, since every call of this
	// service needs one.
	ctx context.Context
	// deviceID is the appliance the session belongs to, which is what every
	// revision this suite writes should name.
	deviceID string
}

// serveLibrary starts the node with all three slices registered and returns the
// library client, authenticated as a reader this node hosts.
//
// It builds the real containers rather than assembling use cases by hand, so
// that what these tests exercise is what cmd/quired runs: the object store the
// configuration named, the authentication interceptor nearest the handler, and
// above it the translation that turns a domain error into a status.
func serveLibrary(t *testing.T) library {
	t.Helper()
	reset(t)
	resetStorage(t)

	cfg := nodeConfig(t)

	identityContainer, err := identitydi.Initialize(cfg, pool, federationdi.Catalogue(pool))
	if err != nil {
		t.Fatalf("building the identity slice: %v", err)
	}

	libraryContainer, err := librarydi.Initialize(t.Context(), cfg, pool, hlc.New())
	if err != nil {
		t.Fatalf("building the library slice: %v", err)
	}

	t.Cleanup(func() { _ = libraryContainer.Close() })

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

	return library{
		client:   quirev1.NewLibraryServiceClient(connection),
		ctx:      bearer(t.Context(), token),
		deviceID: device,
	}
}

// signInWithDevice registers a reader and returns their access token and the
// identifier of the device the session belongs to.
func signInWithDevice(t *testing.T, client quirev1.AuthServiceClient) (token, deviceID string) {
	t.Helper()

	if _, err := client.RegisterUser(t.Context(), &quirev1.RegisterUserRequest{
		LocalName:   "anthony",
		DisplayName: "Anthony Muller",
		Email:       "anthony@example.test",
		Password:    thePassword,
	}); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	session, err := client.Login(t.Context(), &quirev1.LoginRequest{
		LoginId:  &quirev1.LoginRequest_LocalName{LocalName: "anthony"},
		Password: thePassword,
		Device:   &quirev1.DeviceBinding{Name: "Pixel 9", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	return session.GetSession().GetAccessToken(), session.GetDevice().GetId()
}

// createEbook records a work naming payload's digest and returns it.
func (l library) createEbook(t *testing.T, title, payload string) *quirev1.Ebook {
	t.Helper()

	created, err := l.client.CreateEbook(l.ctx, &quirev1.CreateEbookRequest{
		Ebook: &quirev1.Ebook{
			Title:       title,
			Format:      quirev1.EbookFormat_EBOOK_FORMAT_EPUB,
			ContentHash: digestOf(payload),
			SizeBytes:   int64Ptr(int64(len(payload))),
		},
	})
	if err != nil {
		t.Fatalf("CreateEbook: %v", err)
	}

	return created.GetEbook()
}

// upload streams payload to the node and returns the reply.
func (l library) upload(t *testing.T, payload string, declared *quirev1.EbookContent) error {
	t.Helper()

	stream, err := l.client.UploadEbookContent(l.ctx)
	if err != nil {
		return err
	}

	if sendErr := stream.Send(&quirev1.UploadEbookContentRequest{
		Payload: &quirev1.UploadEbookContentRequest_Content{Content: declared},
	}); sendErr != nil && !errors.Is(sendErr, io.EOF) {
		return sendErr
	}

	// The node may have answered before the bytes travel — it does when it
	// already holds the file — and a send into a closed stream is io.EOF,
	// which CloseAndRecv turns into the real status.
	if sendErr := stream.Send(&quirev1.UploadEbookContentRequest{
		Payload: &quirev1.UploadEbookContentRequest_Chunk{Chunk: []byte(payload)},
	}); sendErr != nil && !errors.Is(sendErr, io.EOF) {
		return sendErr
	}

	_, err = stream.CloseAndRecv()

	return err
}

// download reads a work's bytes back, and returns them with what the node said
// they are.
func (l library) download(t *testing.T, ebookID string) (*quirev1.EbookContent, []byte, error) {
	t.Helper()

	stream, err := l.client.DownloadEbookContent(l.ctx,
		&quirev1.DownloadEbookContentRequest{EbookId: ebookID})
	if err != nil {
		return nil, nil, err
	}

	var (
		described *quirev1.EbookContent
		received  []byte
	)

	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return described, received, nil
		}

		if err != nil {
			return described, received, err
		}

		if content := message.GetContent(); content != nil {
			described = content
		}

		received = append(received, message.GetChunk()...)
	}
}

// int64Ptr is how an optional length is set on a request.
func int64Ptr(value int64) *int64 { return &value }

// stringPtr is how an optional identifier is set on a request.
func stringPtr(value string) *string { return &value }

// TestLibraryRoundTrip walks UC01, UC02 and UC03 in the order a reader walks
// them, against a real database, a real object store and a real connection.
//
// The subtests share state on purpose and must run in order: what makes this a
// round trip rather than a set of unit tests is that each step starts from what
// the previous one left behind.
func TestLibraryRoundTrip(t *testing.T) {
	node := serveLibrary(t)

	var (
		work     *quirev1.Ebook
		grouping *quirev1.Collection
	)

	t.Run("the reader records a work and is told the bytes are missing", func(t *testing.T) {
		created, err := node.client.CreateEbook(node.ctx, &quirev1.CreateEbookRequest{
			Ebook: &quirev1.Ebook{
				Title:         "Os Sertões",
				Author:        stringPtr("Euclides da Cunha"),
				Format:        quirev1.EbookFormat_EBOOK_FORMAT_EPUB,
				ContentHash:   theDigest(),
				SizeBytes:     int64Ptr(int64(len(theFile))),
				ExtraMetadata: mustStruct(t, map[string]any{"isbn": "9788535911190"}),
			},
		})
		if err != nil {
			t.Fatalf("CreateEbook: %v", err)
		}

		work = created.GetEbook()

		switch {
		case !created.GetContentMissing():
			t.Error("a node that holds nothing said it already had the file")
		case work.GetRevision().GetDeviceId() != node.deviceID:
			t.Error("the revision does not name the device the session belongs to")
		case work.GetRevision().GetVectorClock().GetEntries()[node.deviceID] != 1:
			t.Error("the causal history does not count the import as an event of that device")
		case work.GetExtraMetadata().GetFields()["isbn"].GetStringValue() != "9788535911190":
			t.Error("the metadata RF05 exists for did not survive the jsonb column")
		}
	})

	t.Run("the work is in the collection", func(t *testing.T) {
		listed, err := node.client.ListEbooks(node.ctx, &quirev1.ListEbooksRequest{})
		if err != nil {
			t.Fatalf("ListEbooks: %v", err)
		}

		if len(listed.GetEbooks()) != 1 || listed.GetEbooks()[0].GetId() != work.GetId() {
			t.Fatalf("the collection holds %d works", len(listed.GetEbooks()))
		}

		if listed.GetNextPageToken() != "" {
			t.Error("a page holding the whole collection reported another one after it")
		}
	})

	t.Run("the description is corrected without touching the rest", func(t *testing.T) {
		updated, err := node.client.UpdateEbook(node.ctx, &quirev1.UpdateEbookRequest{
			EbookId:    work.GetId(),
			Ebook:      &quirev1.Ebook{Author: stringPtr("Euclides Rodrigues Pimenta da Cunha")},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"author"}},
		})
		if err != nil {
			t.Fatalf("UpdateEbook: %v", err)
		}

		switch {
		case updated.GetEbook().GetAuthor() != "Euclides Rodrigues Pimenta da Cunha":
			t.Error("the claimed field was not written")
		case updated.GetEbook().GetTitle() != "Os Sertões":
			t.Error("a field the mask did not name was overwritten")
		case updated.GetEbook().GetContentHash() != theDigest():
			t.Error("editing the description changed what the bytes are")
		case updated.GetEbook().GetRevision().GetVectorClock().GetEntries()[node.deviceID] != 2:
			t.Error("the edit did not tick the causal history, so replication would not carry it")
		}

		work = updated.GetEbook()
	})

	t.Run("the bytes travel and come back", func(t *testing.T) {
		if err := node.upload(t, theFile, &quirev1.EbookContent{
			ContentHash: theDigest(),
			SizeBytes:   int64(len(theFile)),
			MediaType:   "application/epub+zip",
		}); err != nil {
			t.Fatalf("UploadEbookContent: %v", err)
		}

		described, received, err := node.download(t, work.GetId())
		if err != nil {
			t.Fatalf("DownloadEbookContent: %v", err)
		}

		switch {
		case described == nil:
			t.Fatal("the stream did not describe the file before sending it, so a client " +
				"could not verify what it received")
		case described.GetContentHash() != theDigest():
			t.Error("the stream described a different file")
		case string(received) != theFile:
			t.Errorf("the file came back as %q", received)
		}
	})

	t.Run("a second import of the same file needs no transfer", func(t *testing.T) {
		created, err := node.client.CreateEbook(node.ctx, &quirev1.CreateEbookRequest{
			Ebook: &quirev1.Ebook{
				Title:       "Os Sertões, second copy",
				Format:      quirev1.EbookFormat_EBOOK_FORMAT_EPUB,
				ContentHash: theDigest(),
				SizeBytes:   int64Ptr(int64(len(theFile))),
			},
		})
		if err != nil {
			t.Fatalf("CreateEbook: %v", err)
		}

		if created.GetContentMissing() {
			t.Error("the node asked for a file it already holds, so the deduplication " +
				"the digest key exists for is not working")
		}
	})

	t.Run("the reader files it under a shelf", func(t *testing.T) {
		defined, err := node.client.CreateCollection(node.ctx, &quirev1.CreateCollectionRequest{
			Collection: &quirev1.Collection{
				Name: "Literatura brasileira",
				Kind: quirev1.CollectionKind_COLLECTION_KIND_CATEGORY,
			},
		})
		if err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}

		grouping = defined.GetCollection()

		if _, err := node.client.AddEbookToCollection(node.ctx,
			&quirev1.AddEbookToCollectionRequest{
				EbookId: work.GetId(), CollectionId: grouping.GetId(),
			}); err != nil {
			t.Fatalf("AddEbookToCollection: %v", err)
		}

		// Repeating it is not an error, which the contract says and the
		// register makes true.
		if _, err := node.client.AddEbookToCollection(node.ctx,
			&quirev1.AddEbookToCollectionRequest{
				EbookId: work.GetId(), CollectionId: grouping.GetId(),
			}); err != nil {
			t.Fatalf("filing the same work twice: %v", err)
		}
	})

	t.Run("the shelf holds it, and only it", func(t *testing.T) {
		listed, err := node.client.ListEbooks(node.ctx, &quirev1.ListEbooksRequest{
			CollectionId: stringPtr(grouping.GetId()),
		})
		if err != nil {
			t.Fatalf("ListEbooks: %v", err)
		}

		if len(listed.GetEbooks()) != 1 || listed.GetEbooks()[0].GetId() != work.GetId() {
			t.Fatalf("the shelf holds %d works, want the one filed under it — and filing "+
				"twice must not put it there twice (C06)", len(listed.GetEbooks()))
		}

		reverse, err := node.client.ListCollections(node.ctx, &quirev1.ListCollectionsRequest{
			EbookId: stringPtr(work.GetId()),
		})
		if err != nil {
			t.Fatalf("ListCollections: %v", err)
		}

		if len(reverse.GetCollections()) != 1 || reverse.GetCollections()[0].GetId() != grouping.GetId() {
			t.Errorf("the work is on %d shelves", len(reverse.GetCollections()))
		}
	})

	t.Run("taking it off the shelf leaves the work alone", func(t *testing.T) {
		if _, err := node.client.RemoveEbookFromCollection(node.ctx,
			&quirev1.RemoveEbookFromCollectionRequest{
				EbookId: work.GetId(), CollectionId: grouping.GetId(),
			}); err != nil {
			t.Fatalf("RemoveEbookFromCollection: %v", err)
		}

		listed, err := node.client.ListEbooks(node.ctx, &quirev1.ListEbooksRequest{
			CollectionId: stringPtr(grouping.GetId()),
		})
		if err != nil {
			t.Fatalf("ListEbooks: %v", err)
		}

		if len(listed.GetEbooks()) != 0 {
			t.Errorf("the shelf still holds %d works", len(listed.GetEbooks()))
		}

		if _, err := node.client.GetEbook(node.ctx,
			&quirev1.GetEbookRequest{EbookId: work.GetId()}); err != nil {
			t.Errorf("taking the work off a shelf removed the work: %v", err)
		}
	})

	t.Run("deleting the work hides it and keeps the row", func(t *testing.T) {
		if _, err := node.client.DeleteEbook(node.ctx,
			&quirev1.DeleteEbookRequest{EbookId: work.GetId()}); err != nil {
			t.Fatalf("DeleteEbook: %v", err)
		}

		_, err := node.client.GetEbook(node.ctx, &quirev1.GetEbookRequest{EbookId: work.GetId()})
		if status.Code(err) != codes.NotFound {
			t.Errorf("GetEbook after a deletion = %v, want NotFound", err)
		}

		// The row has to survive, or the next node that had not heard about
		// the deletion would resurrect the work.
		var deleted bool

		if err := pool.QueryRow(t.Context(),
			"SELECT deleted FROM library.ebooks WHERE id = $1", work.GetId()).Scan(&deleted); err != nil {
			t.Fatalf("the row went rather than being tombstoned: %v", err)
		}

		if !deleted {
			t.Error("the work was hidden without being marked removed")
		}
	})

	t.Run("the file survives the work that named it", func(t *testing.T) {
		// Another reader on this node may hold the same work, and a second
		// device of this one will ask for it again once the deletion has
		// reached it and been undone.
		var held bool

		if err := pool.QueryRow(t.Context(),
			"SELECT EXISTS (SELECT 1 FROM library.ebook_contents WHERE content_hash = $1)",
			theDigest()).Scan(&held); err != nil {
			t.Fatalf("reading what this node holds: %v", err)
		}

		if !held {
			t.Error("deleting one work took the shared file with it")
		}
	})
}

// mustStruct renders a map as the protobuf value the contract carries.
func mustStruct(t *testing.T, value map[string]any) *structpb.Struct {
	t.Helper()

	rendered, err := structpb.NewStruct(value)
	if err != nil {
		t.Fatalf("building the metadata: %v", err)
	}

	return rendered
}

// TestListEbooksPaginatesByKeyset walks a collection page by page against the
// real statement and the real index.
//
// This is what a unit test cannot check. The double in
// internal/library/application/apptest orders and seeks the way the statement
// is meant to, and an imitation is exactly as good as the reading it was
// written from — including the row comparison, which is the part that decides
// whether a work is skipped or repeated when two share an import instant.
func TestListEbooksPaginatesByKeyset(t *testing.T) {
	node := serveLibrary(t)

	const works = 7

	expected := make(map[string]bool, works)

	for index := range works {
		created := node.createEbook(t, "work "+string(rune('a'+index)), theFile+string(rune('a'+index)))
		expected[created.GetId()] = false
	}

	seen := make([]string, 0, works)
	token := ""

	for range works {
		page, err := node.client.ListEbooks(node.ctx, &quirev1.ListEbooksRequest{
			PageSize: 3, PageToken: token,
		})
		if err != nil {
			t.Fatalf("ListEbooks: %v", err)
		}

		for _, work := range page.GetEbooks() {
			if expected[work.GetId()] {
				t.Errorf("%q came back on two pages", work.GetTitle())
			}

			expected[work.GetId()] = true
			seen = append(seen, work.GetId())
		}

		token = page.GetNextPageToken()
		if token == "" {
			break
		}
	}

	if len(seen) != works {
		t.Errorf("walking the cursor saw %d works, want %d", len(seen), works)
	}

	for id, found := range expected {
		if !found {
			t.Errorf("%s was never returned by any page", id)
		}
	}
}

// TestDeletingAWorkClearsItsFilings is the transaction, checked against the
// database rather than against a counter.
//
// The two tombstones replicate independently, so a node that wrote one without
// the other would show the work on a shelf it is no longer in — here until
// somebody noticed, and on every peer until they received the missing half.
func TestDeletingAWorkClearsItsFilings(t *testing.T) {
	node := serveLibrary(t)

	work := node.createEbook(t, "Os Sertões", theFile)

	defined, err := node.client.CreateCollection(node.ctx, &quirev1.CreateCollectionRequest{
		Collection: &quirev1.Collection{Name: "Literatura"},
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err := node.client.AddEbookToCollection(node.ctx, &quirev1.AddEbookToCollectionRequest{
		EbookId: work.GetId(), CollectionId: defined.GetCollection().GetId(),
	}); err != nil {
		t.Fatalf("AddEbookToCollection: %v", err)
	}

	if _, err := node.client.DeleteEbook(node.ctx,
		&quirev1.DeleteEbookRequest{EbookId: work.GetId()}); err != nil {
		t.Fatalf("DeleteEbook: %v", err)
	}

	var (
		deleted  bool
		deviceID uuid.UUID
	)

	if err := pool.QueryRow(t.Context(),
		"SELECT deleted, device_id FROM library.ebook_collections WHERE ebook_id = $1",
		work.GetId()).Scan(&deleted, &deviceID); err != nil {
		t.Fatalf("reading the filing: %v", err)
	}

	if !deleted {
		t.Error("the work is still on the shelf, and every peer would be told so")
	}

	if deviceID.String() != node.deviceID {
		t.Error("the filing's tombstone does not name the device that caused it, so the " +
			"tie-break against a concurrent filing has one half")
	}
}

// TestDeletingAGroupingKeepsTheWorks is the other half of the same rule: the
// works survive their shelf.
func TestDeletingAGroupingKeepsTheWorks(t *testing.T) {
	node := serveLibrary(t)

	work := node.createEbook(t, "Os Sertões", theFile)

	defined, err := node.client.CreateCollection(node.ctx, &quirev1.CreateCollectionRequest{
		Collection: &quirev1.Collection{Name: "Literatura"},
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err = node.client.AddEbookToCollection(node.ctx, &quirev1.AddEbookToCollectionRequest{
		EbookId: work.GetId(), CollectionId: defined.GetCollection().GetId(),
	}); err != nil {
		t.Fatalf("AddEbookToCollection: %v", err)
	}

	if _, err = node.client.DeleteCollection(node.ctx, &quirev1.DeleteCollectionRequest{
		CollectionId: defined.GetCollection().GetId(),
	}); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	if _, err = node.client.GetEbook(node.ctx,
		&quirev1.GetEbookRequest{EbookId: work.GetId()}); err != nil {
		t.Errorf("deleting the shelf deleted what was on it: %v", err)
	}

	listed, err := node.client.ListCollections(node.ctx, &quirev1.ListCollectionsRequest{})
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}

	if len(listed.GetCollections()) != 0 {
		t.Errorf("the deleted grouping is still listed")
	}
}

// TestUploadRefusesWhatTheNodeMustNotStore covers the two refusals the object
// store's integrity rests on, against a real store.
func TestUploadRefusesWhatTheNodeMustNotStore(t *testing.T) {
	node := serveLibrary(t)
	node.createEbook(t, "Os Sertões", theFile)

	t.Run("bytes that are not what was declared", func(t *testing.T) {
		err := node.upload(t, "something else entirely", &quirev1.EbookContent{
			ContentHash: theDigest(),
			SizeBytes:   int64(len("something else entirely")),
			MediaType:   "application/epub+zip",
		})

		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("UploadEbookContent = %v, want InvalidArgument", err)
		}

		// Every later reader of an object trusts its name, so nothing may be
		// stored under a digest that was not checked against the bytes.
		var held bool

		if err := pool.QueryRow(t.Context(),
			"SELECT EXISTS (SELECT 1 FROM library.ebook_contents WHERE content_hash = $1)",
			theDigest()).Scan(&held); err != nil {
			t.Fatalf("reading what this node holds: %v", err)
		}

		if held {
			t.Error("the node recorded a file it refused")
		}
	})

	// C16: the upload carries no work identifier, so without this check the
	// object store is writable by any authenticated reader under any name.
	t.Run("a digest no work of the caller's names", func(t *testing.T) {
		unclaimed := "bytes nobody recorded a work for"

		err := node.upload(t, unclaimed, &quirev1.EbookContent{
			ContentHash: digestOf(unclaimed),
			SizeBytes:   int64(len(unclaimed)),
			MediaType:   "application/epub+zip",
		})

		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("UploadEbookContent = %v, want FailedPrecondition", err)
		}

		if reason := reasonOf(err); reason != "unclaimed_content" {
			t.Errorf("the refusal is coded %q", reason)
		}
	})

	t.Run("a file larger than the node accepts", func(t *testing.T) {
		err := node.upload(t, theFile, &quirev1.EbookContent{
			ContentHash: theDigest(),
			SizeBytes:   1024 * 1024,
			MediaType:   "application/epub+zip",
		})

		if status.Code(err) != codes.ResourceExhausted {
			t.Errorf("UploadEbookContent = %v, want ResourceExhausted", err)
		}
	})
}

// TestDownloadingAFileThisNodeDoesNotHold is the state a replica without files
// is in for every work it has, and it must be distinguishable from a work that
// does not exist (D02).
func TestDownloadingAFileThisNodeDoesNotHold(t *testing.T) {
	node := serveLibrary(t)
	work := node.createEbook(t, "Os Sertões", theFile)

	_, _, err := node.download(t, work.GetId())

	if status.Code(err) != codes.NotFound {
		t.Fatalf("DownloadEbookContent = %v, want NotFound", err)
	}

	if reason := reasonOf(err); reason != "content_not_found" {
		t.Errorf("the reply is coded %q, want a client to be able to tell this from a work "+
			"that does not exist", reason)
	}
}

// TestTheLibraryStatements exercises the repositories directly, so that the
// constraints and the row lock are checked against the database rather than
// against the doubles that imitate them.
func TestTheLibraryStatements(t *testing.T) {
	reset(t)

	manager := persist.NewManager(pool)
	works := ebookrepository.New(manager)
	collections := collectionrepository.New(manager)
	memberships := membershiprepository.New(manager)

	reader, device := seedReader(t)
	at := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("a work round trips through every column", func(t *testing.T) {
		work, err := ebook.New(reader,
			&ebook.Details{
				Title: "Os Sertões", Author: "Euclides da Cunha",
				Publisher: "Laemmert", Language: "pt-BR",
				Extra: ebook.Metadata{"isbn": "9788535911190"},
			},
			&ebook.File{Format: ebook.FormatEPUB, Hash: ebook.ContentHash(theDigest()), Size: 1024},
			device, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = works.Create(t.Context(), work); err != nil {
			t.Fatalf("Create: %v", err)
		}

		stored, err := works.GetByID(t.Context(), work.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}

		switch {
		case stored.Author != "Euclides da Cunha" || stored.Language != "pt-BR":
			t.Errorf("the description came back as %+v", stored.Details)
		case stored.Extra["isbn"] != "9788535911190":
			t.Error("the jsonb column lost the metadata RF05 exists for")
		case !stored.Revision.UpdatedAt.Equal(work.Revision.UpdatedAt):
			t.Errorf("the tie-break timestamp came back as %s, want the %s that was written — "+
				"a value the column cannot hold would decide conflicts differently in memory "+
				"and on disk", stored.Revision.UpdatedAt, work.Revision.UpdatedAt)
		case !stored.Revision.VectorClock.Equal(work.Revision.VectorClock):
			t.Error("the causal history did not survive the jsonb column")
		case stored.Revision.DeviceID != device:
			t.Error("the revision lost the device whose write the row reflects")
		}
	})

	// C06: Quadro 20 has no uniqueness constraint, so nothing in the
	// specification stops the same work from being filed twice in the same
	// grouping — which is exactly what two offline devices will do.
	t.Run("the pair of a filing is unique", func(t *testing.T) {
		work := seedEbook(t, works, reader, device, at, "a work to file")
		grouping := seedCollection(t, collections, reader, device, at, "a shelf")

		first, err := membership.New(work.ID, grouping.ID, device, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err = memberships.Create(t.Context(), first); err != nil {
			t.Fatalf("Create: %v", err)
		}

		second, err := membership.New(work.ID, grouping.ID, device, at)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		err = memberships.Create(t.Context(), second)
		if !errors.Is(err, errs.KindAlreadyExists) {
			t.Fatalf("Create of a second filing of the same pair = %v, want an already exists", err)
		}

		if code := errs.CodeOf(err); code != membership.CodeAlreadyFiled {
			t.Errorf("the refusal is coded %q, so a caller could not tell it from any other "+
				"write failure and would fail instead of setting the register", code)
		}
	})

	t.Run("a digest the column will not hold is refused", func(t *testing.T) {
		work, err := ebook.New(reader, &ebook.Details{Title: "a work"},
			&ebook.File{Format: ebook.FormatEPUB, Hash: "NOTAHASH", Size: 1},
			device, at)
		if err == nil {
			// The value object refuses it first, which is the point: the
			// constraint is the second line and not the only one.
			if createErr := works.Create(t.Context(), work); createErr == nil {
				t.Error("a digest that is not one reached the table")
			}

			return
		}

		if !errors.Is(err, errs.KindInvalidArgument) {
			t.Errorf("New = %v, want an invalid argument", err)
		}
	})

	t.Run("a page is ordered and cursored by the index", func(t *testing.T) {
		reset(t)

		reader, device := seedReader(t)
		base := time.Now().UTC().Truncate(time.Microsecond)

		// One microsecond apart, which is the resolution the column has and
		// therefore the interval the identifier tie-break has to survive.
		for index := range 5 {
			seedEbook(t, works, reader, device, base.Add(time.Duration(index)*time.Microsecond),
				"work "+string(rune('a'+index)))
		}

		page, cursor, err := works.List(t.Context(), &ebook.Query{UserID: reader, Size: 2})
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		if len(page) != 2 {
			t.Fatalf("the page holds %d works, want 2", len(page))
		}

		if cursor.IsZero() {
			t.Fatal("a page with three works after it reported none")
		}

		next, _, err := works.List(t.Context(), &ebook.Query{UserID: reader, Size: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		if len(next) == 0 || next[0].ID == page[1].ID {
			t.Error("the cursor repeated the row it was taken from")
		}

		if !page[0].ImportedAt.After(page[1].ImportedAt) {
			t.Error("the page is not ordered most recently imported first")
		}
	})
}

// TestFilingAWorkSerializesWithDeletingTheGrouping is the row lock, checked by
// making the two calls contend.
//
// Both read the grouping and then write a row that references it, and under
// READ COMMITTED neither read can see the other's write. Without the lock the
// filing would be written against a grouping tombstoned in between, and the
// work would sit on a shelf no reply mentions and no later deletion would
// clear.
func TestFilingAWorkSerializesWithDeletingTheGrouping(t *testing.T) {
	reset(t)
	resetStorage(t)

	manager := persist.NewManager(pool)

	works := ebookrepository.New(manager)
	collections := collectionrepository.New(manager)
	memberships := membershiprepository.New(manager)
	clock := clockservice.New(hlc.New())

	reader, device := seedReader(t)
	at := time.Now().UTC().Truncate(time.Microsecond)

	work := seedEbook(t, works, reader, device, at, "a work to file")
	grouping := seedCollection(t, collections, reader, device, at, "a shelf")

	filing := addtocollectionusecase.New(works, collections, memberships, clock, manager)
	deleting := deletecollectionusecase.New(collections, memberships, clock, manager)

	// The two run at the same time. Whichever commits first decides, and the
	// other must see what it committed rather than a snapshot from before it.
	var wait sync.WaitGroup

	wait.Add(2)

	var fileErr, deleteErr error

	go func() {
		defer wait.Done()

		_, fileErr = filing.Execute(t.Context(), addtocollectionusecase.Input{
			UserID: reader, DeviceID: device, EbookID: work.ID, CollectionID: grouping.ID,
		})
	}()

	go func() {
		defer wait.Done()

		_, deleteErr = deleting.Execute(t.Context(), deletecollectionusecase.Input{
			UserID: reader, DeviceID: device, CollectionID: grouping.ID,
		})
	}()

	wait.Wait()

	if deleteErr != nil {
		t.Fatalf("deleting the grouping: %v", deleteErr)
	}

	// The filing either happened before the deletion — in which case the
	// deletion cleared it — or it was refused because the grouping was gone.
	// What must not happen is a filing that stands under a deleted grouping.
	if fileErr != nil && !errors.Is(fileErr, errs.KindNotFound) {
		t.Fatalf("filing the work: %v", fileErr)
	}

	stored, err := memberships.GetByPair(t.Context(), work.ID, grouping.ID)
	if err != nil {
		if errors.Is(err, errs.KindNotFound) {
			return
		}

		t.Fatalf("GetByPair: %v", err)
	}

	if stored.IsFiled() {
		t.Error("the work is filed under a grouping that was deleted, and no later deletion " +
			"would clear it")
	}
}

// seedReader registers a reader and a device directly, for the tests that
// exercise the repositories rather than the service.
func seedReader(t *testing.T) (readerID, deviceID uuid.UUID) {
	t.Helper()

	var serverID uuid.UUID

	if err := pool.QueryRow(t.Context(),
		`INSERT INTO federation.servers (domain, base_url, is_local, active)
		 VALUES ($1, $2, true, true) RETURNING id`,
		testServerName, "http://"+testServerName).Scan(&serverID); err != nil {
		t.Fatalf("seeding this node's own row: %v", err)
	}

	if err := pool.QueryRow(t.Context(),
		`INSERT INTO identity.users (origin_server_id, local_name, display_name)
		 VALUES ($1, 'anthony', 'Anthony') RETURNING id`, serverID).Scan(&readerID); err != nil {
		t.Fatalf("seeding a reader: %v", err)
	}

	if err := pool.QueryRow(t.Context(),
		`INSERT INTO identity.devices (user_id, name, platform)
		 VALUES ($1, 'Pixel 9', 'android') RETURNING id`, readerID).Scan(&deviceID); err != nil {
		t.Fatalf("seeding a device: %v", err)
	}

	return readerID, deviceID
}

// seedEbook stores a work directly.
func seedEbook(
	t *testing.T,
	works *ebookrepository.Repository,
	reader, device uuid.UUID,
	at time.Time,
	title string,
) *ebook.Ebook {
	t.Helper()

	work, err := ebook.New(reader, &ebook.Details{Title: ebook.Title(title)},
		&ebook.File{Format: ebook.FormatEPUB, Hash: ebook.ContentHash(digestOf(title)), Size: 1024},
		device, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := works.Create(t.Context(), work); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return work
}

// seedCollection stores a grouping directly.
func seedCollection(
	t *testing.T,
	collections *collectionrepository.Repository,
	reader, device uuid.UUID,
	at time.Time,
	name string,
) *collection.Collection {
	t.Helper()

	grouping, err := collection.New(reader,
		&collection.Details{Name: collection.Name(name), Kind: collection.KindCollection},
		device, at)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := collections.Create(t.Context(), grouping); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return grouping
}
