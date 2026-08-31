//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	federationdi "github.com/anthonyvsmuller/quire/internal/federation/di"
	federationreplica "github.com/anthonyvsmuller/quire/internal/federation/domain/replica"
	federationserver "github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	replicarepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/replica"
	serverrepository "github.com/anthonyvsmuller/quire/internal/federation/infra/repository/server"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	identitydi "github.com/anthonyvsmuller/quire/internal/identity/di"
	librarydi "github.com/anthonyvsmuller/quire/internal/library/di"
	readingdi "github.com/anthonyvsmuller/quire/internal/reading/di"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/grpcx"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/logging"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	"github.com/anthonyvsmuller/quire/internal/sync/application/usecase/replicate"
	syncdi "github.com/anthonyvsmuller/quire/internal/sync/di"
	"github.com/anthonyvsmuller/quire/internal/sync/domain/operation"
	deliveryrepository "github.com/anthonyvsmuller/quire/internal/sync/infra/repository/delivery"
	operationrepository "github.com/anthonyvsmuller/quire/internal/sync/infra/repository/operation"
	syncclock "github.com/anthonyvsmuller/quire/internal/sync/infra/service/clock"
)

// synchronization is the node's whole gRPC surface with the sync service
// registered, a reader signed in, and one work already in their collection.
type synchronization struct {
	reading
	client quirev1.SyncServiceClient
	// userID is the reader, which the peer-facing half of the slice names
	// explicitly and which the delivery queue is scoped by.
	userID string
	// authClient is kept so that a test can bind a second device, which is the
	// only way to author operations RN10 will refuse.
	authClient quirev1.AuthServiceClient
	// connection is the open channel, so that a test can open a stream on it.
	connection *grpc.ClientConn
}

// serveSync starts the node with all five slices registered and returns the
// sync client, authenticated as a reader this node hosts.
//
// It builds the real containers rather than assembling use cases by hand, so
// that what these tests exercise is what cmd/quired runs — the reconciler over
// the library and reading repositories included, which is the wiring this slice
// needs and no other does.
func serveSync(t *testing.T) synchronization {
	t.Helper()
	reset(t)
	resetStorage(t)

	cfg := nodeConfig(t)
	clock := hlc.New()

	identityContainer, err := identitydi.Initialize(cfg, pool, federationdi.Catalogue(pool), logging.Discard())
	if err != nil {
		t.Fatalf("building the identity slice: %v", err)
	}

	federationContainer := federationdi.Initialize(cfg, pool, identityContainer.Migration)

	libraryContainer, err := librarydi.Initialize(t.Context(), cfg, pool, clock, logging.Discard())
	if err != nil {
		t.Fatalf("building the library slice: %v", err)
	}

	t.Cleanup(func() { _ = libraryContainer.Close() })

	readingContainer := readingdi.Initialize(pool, libraryContainer.Ebooks, clock)

	syncContainer, err := syncdi.Initialize(cfg, pool, clock,
		federationContainer.Servers, federationContainer.Authorizations, &syncdi.Records{
			Works:     libraryContainer.Ebooks,
			Groupings: libraryContainer.Collections,
			Filings:   libraryContainer.Memberships,
			Marks:     readingContainer.Annotations,
			Positions: readingContainer.Progress,
		}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("building the sync slice: %v", err)
	}

	t.Cleanup(func() { _ = syncContainer.Close() })

	grpcServer, err := grpcx.New(t.Context(), &cfg.Server,
		grpcx.WithChain(grpcx.NewChain(logging.Discard())),
		grpcx.WithUnaryInterceptors(identityContainer.Interceptor.Unary()),
		grpcx.WithStreamInterceptors(identityContainer.Interceptor.Stream()),
	)
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}

	identityContainer.Service.Register(grpcServer.Registrar())
	federationContainer.Service.Register(grpcServer.Registrar())
	libraryContainer.Service.Register(grpcServer.Registrar())
	readingContainer.Service.Register(grpcServer.Registrar())
	syncContainer.Service.Register(grpcServer.Registrar())

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

		if stopped := <-served; stopped != nil {
			t.Errorf("Serve returned %v", stopped)
		}
	})

	authClient := quirev1.NewAuthServiceClient(connection)
	token, device := signInWithDevice(t, authClient)

	node := synchronization{
		reading: reading{
			library: library{
				client:   quirev1.NewLibraryServiceClient(connection),
				ctx:      bearer(t.Context(), token),
				deviceID: device,
			},
			client: quirev1.NewReadingServiceClient(connection),
		},
		client:     quirev1.NewSyncServiceClient(connection),
		authClient: authClient,
		connection: connection,
	}

	whoami, err := quirev1.NewAuthServiceClient(connection).GetUser(node.ctx, &quirev1.GetUserRequest{})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	node.userID = whoami.GetUser().GetId()
	node.ebookID = node.createEbook(t, "Os Sertões", theFile).GetId()

	return node
}

// change is one operation as a device authors it.
//
// The clock is a counter for the authoring device alone, which is what a device
// writing on its own produces: an operation whose history does not count its own
// author is one no node can place, and the entity refuses it.
func (s *synchronization) change(
	t *testing.T,
	entity quirev1.TargetEntity,
	target string,
	kind quirev1.OperationKind,
	counter uint64,
	at time.Time,
	delta map[string]any,
) *quirev1.Operation {
	t.Helper()

	return s.changeBy(t, s.deviceID, entity, target, kind, counter, at, delta)
}

// changeBy is [synchronization.change] authored by a device the caller names,
// which is what RN10 is checked against.
func (s *synchronization) changeBy(
	t *testing.T,
	device string,
	entity quirev1.TargetEntity,
	target string,
	kind quirev1.OperationKind,
	counter uint64,
	at time.Time,
	delta map[string]any,
) *quirev1.Operation {
	t.Helper()

	claimed, err := structpb.NewStruct(delta)
	if err != nil {
		t.Fatalf("building a delta: %v", err)
	}

	return &quirev1.Operation{
		Id:           uuid.New().String(),
		DeviceId:     device,
		TargetEntity: entity,
		TargetId:     target,
		Operation:    kind,
		Delta:        claimed,
		VectorClock:  &quirev1.VectorClock{Entries: map[string]uint64{device: counter}},
		CreatedAt:    &quirev1.HybridTimestamp{UnixMicros: at.UnixMicro()},
	}
}

// push offers the operations and returns the verdicts.
func (s *synchronization) push(
	t *testing.T, operations ...*quirev1.Operation,
) *quirev1.PushOperationsResponse {
	t.Helper()

	pushed, err := s.client.PushOperations(s.ctx,
		&quirev1.PushOperationsRequest{Operations: operations})
	if err != nil {
		t.Fatalf("PushOperations: %v", err)
	}

	return pushed
}

// pull reads everything after the cursor.
func (s *synchronization) pull(t *testing.T, after int64) *quirev1.PullOperationsResponse {
	t.Helper()

	pulled, err := s.client.PullOperations(s.ctx,
		&quirev1.PullOperationsRequest{AfterPosition: after})
	if err != nil {
		t.Fatalf("PullOperations: %v", err)
	}

	return pulled
}

// authored is the instant the operations below are stamped at.
//
// It is an hour behind the machine rather than a date written out, and the
// reason is the node's own clock: an instant more than five minutes ahead of it
// is beyond the drift ceiling and is not adopted, so a fixed date would test
// the ceiling instead of the reconciler and would say so in the log on every
// push. An hour behind is a device that has been offline, which is the case
// this whole slice exists for.
var authored = time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

// theShelf is the kind of grouping the operations below create.
const theShelf = "collection"

// TestSyncRoundTrip walks UC09 and UC11 in the order a device walks them,
// against a real database and over a real connection.
//
// The subtests share state on purpose and must run in order: what makes this a
// round trip rather than a set of unit tests is that each step starts from what
// the previous one left in the database.
func TestSyncRoundTrip(t *testing.T) {
	node := serveSync(t)

	work := uuid.New().String()

	var insert *quirev1.Operation

	t.Run("a device pushes a work it created while it was disconnected", func(t *testing.T) {
		insert = node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, work,
			quirev1.OperationKind_OPERATION_KIND_INSERT, 1, authored, map[string]any{
				"title":        "Vidas Secas",
				"format":       "epub",
				"content_hash": digestOf("the bytes of Vidas Secas"),
			})

		pushed := node.push(t, insert)

		if outcome := pushed.GetResults()[0].GetOutcome(); outcome !=
			quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
			t.Fatalf("the change was %s: %s", outcome, pushed.GetResults()[0].GetDetail())
		}

		if pushed.GetLastPosition() != 1 {
			t.Errorf("the log ends at %d, want the change numbered", pushed.GetLastPosition())
		}
	})

	t.Run("the reconciler wrote the work into the reader's collection", func(t *testing.T) {
		got, err := node.library.client.GetEbook(node.ctx, &quirev1.GetEbookRequest{EbookId: work})
		if err != nil {
			t.Fatalf("GetEbook: %v", err)
		}

		switch {
		case got.GetEbook().GetTitle() != "Vidas Secas":
			t.Errorf("the work came out as %q", got.GetEbook().GetTitle())
		case got.GetEbook().GetRevision().GetDeviceId() != node.deviceID:
			t.Error("the record does not name the device whose write it is")
		case got.GetEbook().GetRevision().GetUpdatedAt().GetUnixMicros() != authored.UnixMicro():
			t.Error("the record was stamped by this node rather than by the device that wrote it")
		}
	})

	t.Run("the device pulls its own log back with a position on it", func(t *testing.T) {
		pulled := node.pull(t, 0)

		if len(pulled.GetOperations()) != 1 {
			t.Fatalf("the page carries %d changes, want the one that was pushed", len(pulled.GetOperations()))
		}

		got := pulled.GetOperations()[0]

		switch {
		case got.GetId() != insert.GetId():
			t.Error("the change came back under a different identifier, which no node would recognize")
		case got.GetPosition() != 1:
			t.Errorf("the change came back at position %d", got.GetPosition())
		case got.GetDelta().GetFields()["title"].GetStringValue() != "Vidas Secas":
			t.Errorf("the delta came back as %v", got.GetDelta().AsMap())
		case pulled.GetHasMore():
			t.Error("the whole log came back and the reply says there is more")
		}
	})

	t.Run("the same change offered again is a duplicate", func(t *testing.T) {
		pushed := node.push(t, insert)

		if outcome := pushed.GetResults()[0].GetOutcome(); outcome !=
			quirev1.OperationOutcome_OPERATION_OUTCOME_DUPLICATE {
			t.Errorf("the second offer was %s, want a duplicate", outcome)
		}

		// It consumed a position doing so, which is what the statement does
		// and what makes a gap in the log ordinary.
		if pushed.GetLastPosition() != 2 {
			t.Errorf("the log ends at %d, want the duplicate to have consumed a number",
				pushed.GetLastPosition())
		}
	})

	t.Run("a version the record already reflects is superseded", func(t *testing.T) {
		stale := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, work,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, 1, authored.Add(-time.Hour),
			map[string]any{"title": "São Bernardo"})

		pushed := node.push(t, stale)

		if outcome := pushed.GetResults()[0].GetOutcome(); outcome !=
			quirev1.OperationOutcome_OPERATION_OUTCOME_SUPERSEDED {
			t.Fatalf("the stale change was %s, want superseded", outcome)
		}

		got, err := node.library.client.GetEbook(node.ctx, &quirev1.GetEbookRequest{EbookId: work})
		if err != nil {
			t.Fatalf("GetEbook: %v", err)
		}

		if got.GetEbook().GetTitle() != "Vidas Secas" {
			t.Errorf("a superseded change was applied anyway: the title is now %q", got.GetEbook().GetTitle())
		}

		// And it is kept, because a later node has to reach the same
		// conclusion from the same history.
		if !holdsOperation(t, node.pull(t, 0), stale.GetId()) {
			t.Error("a superseded change was dropped from the log")
		}
	})

	t.Run("a causally later change writes only the fields it claims", func(t *testing.T) {
		later := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, work,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, 2, authored.Add(time.Minute),
			map[string]any{"author": "Graciliano Ramos"})

		if outcome := node.push(t, later).GetResults()[0].GetOutcome(); outcome !=
			quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
			t.Fatalf("the later change was %s", outcome)
		}

		got, err := node.library.client.GetEbook(node.ctx, &quirev1.GetEbookRequest{EbookId: work})
		if err != nil {
			t.Fatalf("GetEbook: %v", err)
		}

		switch {
		case got.GetEbook().GetAuthor() != "Graciliano Ramos":
			t.Errorf("the claimed field came out as %q", got.GetEbook().GetAuthor())
		case got.GetEbook().GetTitle() != "Vidas Secas":
			t.Errorf("a field the change did not claim came out as %q", got.GetEbook().GetTitle())
		}
	})

	t.Run("a change the node refuses leaves nothing behind", func(t *testing.T) {
		before := node.pull(t, 0)

		refused := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, uuid.New().String(),
			quirev1.OperationKind_OPERATION_KIND_UPDATE, 3, authored.Add(2*time.Minute),
			map[string]any{"title": "a work this node does not hold"})
		sound := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, work,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, 4, authored.Add(3*time.Minute),
			map[string]any{"publisher": "Record"})

		pushed := node.push(t, refused, sound)

		switch {
		case pushed.GetResults()[0].GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_REJECTED:
			t.Errorf("the refused change was %s", pushed.GetResults()[0].GetOutcome())
		case pushed.GetResults()[0].GetDetail() == "":
			t.Error("the rejection says nothing, and the operator has nothing to act on")
		case pushed.GetResults()[1].GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED:
			t.Error("one refused change took the rest of the batch with it")
		}

		after := node.pull(t, 0)

		if holdsOperation(t, after, refused.GetId()) {
			t.Error("a refused change was stored anyway, so the unit of work did not unwind")
		}

		if !holdsOperation(t, after, sound.GetId()) {
			t.Error("the change beside it was rolled back with it")
		}

		if len(after.GetOperations()) != len(before.GetOperations())+1 {
			t.Errorf("the log grew by %d, want only the change that was applied",
				len(after.GetOperations())-len(before.GetOperations()))
		}
	})
}

// RN10: a batch with a forged author in it is not a batch any of which should
// be trusted, so it fails whole rather than reporting a per-change rejection.
func TestSyncRefusesABatchTheCallerDidNotAuthor(t *testing.T) {
	node := serveSync(t)

	// A second device, bound to the same reader, so that the identifier is one
	// the schema will accept and the interceptor will not.
	second, err := node.authClient.RegisterDevice(node.ctx, &quirev1.RegisterDeviceRequest{
		Name:     "Tablet",
		Platform: "ipados",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	mine := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
		quirev1.OperationKind_OPERATION_KIND_UPDATE, 1, authored, map[string]any{"title": "mine"})
	theirs := node.changeBy(t, second.GetDevice().GetId(),
		quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
		quirev1.OperationKind_OPERATION_KIND_UPDATE, 1, authored, map[string]any{"title": "theirs"})

	_, err = node.client.PushOperations(node.ctx,
		&quirev1.PushOperationsRequest{Operations: []*quirev1.Operation{mine, theirs}})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("PushOperations = %v, want a permission denied", err)
	}

	if pulled := node.pull(t, 0); len(pulled.GetOperations()) != 0 {
		t.Errorf("the batch was refused after %d of it had been stored", len(pulled.GetOperations()))
	}
}

// The cursor is a single number because a caller that has seen position N has
// necessarily seen every position below it, which is what allocating the number
// inside the writing transaction buys (C08). This walks a log in pages against
// the real allocator and the real index.
func TestSyncWalkOfTheLogSeesEveryChangeExactlyOnce(t *testing.T) {
	node := serveSync(t)

	const changes = 25

	written := make(map[string]bool, changes)

	for counter := range changes {
		pushed := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, uint64(counter)+1,
			authored.Add(time.Duration(counter)*time.Second),
			map[string]any{"title": "Os Sertões"})

		node.push(t, pushed)

		written[pushed.GetId()] = true
	}

	seen := make(map[string]int, changes)
	cursor := int64(0)

	for range changes {
		page, err := node.client.PullOperations(node.ctx, &quirev1.PullOperationsRequest{
			AfterPosition: cursor,
			Limit:         4,
		})
		if err != nil {
			t.Fatalf("PullOperations: %v", err)
		}

		for _, operation := range page.GetOperations() {
			seen[operation.GetId()]++

			if operation.GetPosition() <= cursor {
				t.Fatalf("the page went backwards: position %d after a cursor of %d",
					operation.GetPosition(), cursor)
			}
		}

		cursor = page.GetLastPosition()

		if !page.GetHasMore() {
			break
		}
	}

	if len(seen) != changes {
		t.Fatalf("the walk saw %d changes, want all %d", len(seen), changes)
	}

	for id, count := range seen {
		switch {
		case count != 1:
			t.Errorf("%s came back %d times", id, count)
		case !written[id]:
			t.Errorf("%s was never written", id)
		}
	}
}

// The position is allocated from a row lock held until commit, so two devices
// pushing at the same moment cannot be given the same number — and a reader
// that pulls afterwards sees every one of them.
func TestSyncNumbersConcurrentPushesWithoutCollidingOrSkipping(t *testing.T) {
	node := serveSync(t)

	const writers = 8

	var group sync.WaitGroup

	group.Add(writers)

	for writer := range writers {
		go func() {
			defer group.Done()

			racing := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
				quirev1.OperationKind_OPERATION_KIND_UPDATE, uint64(writer)+1,
				authored.Add(time.Duration(writer)*time.Second),
				map[string]any{"title": "Os Sertões"})

			if _, pushErr := node.client.PushOperations(node.ctx,
				&quirev1.PushOperationsRequest{Operations: []*quirev1.Operation{racing}}); pushErr != nil {
				t.Errorf("PushOperations: %v", pushErr)
			}
		}()
	}

	group.Wait()

	pulled := node.pull(t, 0)
	if len(pulled.GetOperations()) != writers {
		t.Fatalf("the log holds %d changes, want the %d that were pushed",
			len(pulled.GetOperations()), writers)
	}

	positions := make(map[int64]bool, writers)

	for _, operation := range pulled.GetOperations() {
		if positions[operation.GetPosition()] {
			t.Errorf("two changes were numbered %d", operation.GetPosition())
		}

		positions[operation.GetPosition()] = true
	}
}

// The five entities the node replicates, through the five repositories two
// other slices own. The two associative ones are addressed by their natural
// key and not by the identifier the operation names, which is C18.
func TestSyncReconcilesEveryReplicatedEntity(t *testing.T) {
	node := serveSync(t)

	grouping := uuid.New().String()
	work := node.ebookID
	mark := uuid.New().String()

	pushed := node.push(t,
		node.change(t, quirev1.TargetEntity_TARGET_ENTITY_COLLECTION, grouping,
			quirev1.OperationKind_OPERATION_KIND_INSERT, 1, authored,
			map[string]any{"name": "Cabeceira", "kind": theShelf}),
		node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION, uuid.New().String(),
			quirev1.OperationKind_OPERATION_KIND_INSERT, 2, authored.Add(time.Second),
			map[string]any{"ebook_id": work, "collection_id": grouping}),
		node.change(t, quirev1.TargetEntity_TARGET_ENTITY_ANNOTATION, mark,
			quirev1.OperationKind_OPERATION_KIND_INSERT, 3, authored.Add(2*time.Second),
			map[string]any{
				"ebook_id": work,
				"kind":     "note",
				"text":     "o sertanejo é, antes de tudo, um forte",
				"locator":  thePassage,
			}),
		node.change(t, quirev1.TargetEntity_TARGET_ENTITY_READING_PROGRESS, uuid.New().String(),
			quirev1.OperationKind_OPERATION_KIND_INSERT, 4, authored.Add(3*time.Second),
			map[string]any{"ebook_id": work, "locator": thePassage, "percent": 41.5}),
	)

	for index, result := range pushed.GetResults() {
		if result.GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
			t.Fatalf("change %d was %s: %s", index, result.GetOutcome(), result.GetDetail())
		}
	}

	groupings, err := node.library.client.ListCollections(node.ctx,
		&quirev1.ListCollectionsRequest{EbookId: &work})
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}

	if len(groupings.GetCollections()) != 1 || groupings.GetCollections()[0].GetId() != grouping {
		t.Errorf("the work is filed under %d groupings, want the one the batch created",
			len(groupings.GetCollections()))
	}

	marks, err := node.reading.client.ListAnnotations(node.ctx,
		&quirev1.ListAnnotationsRequest{EbookId: work})
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}

	if len(marks.GetAnnotations()) != 1 || marks.GetAnnotations()[0].GetId() != mark {
		t.Errorf("the work holds %d marks, want the one the batch created", len(marks.GetAnnotations()))
	}

	positions, err := node.reading.client.ListReadingProgress(node.ctx,
		&quirev1.ListReadingProgressRequest{EbookId: work})
	if err != nil {
		t.Fatalf("ListReadingProgress: %v", err)
	}

	if len(positions.GetProgress()) != 1 {
		t.Fatalf("the work holds %d positions, want the one the batch created",
			len(positions.GetProgress()))
	}

	// The position is addressed by the work and the authoring device, never by
	// the identifier the operation names: a surrogate key minted per replica
	// cannot identify a replicated record (C18).
	if positions.GetProgress()[0].GetDeviceId() != node.deviceID {
		t.Error("the position was written under a device other than the one that authored the change")
	}
}

// A filing carries a surrogate key each replica mints for itself, so two
// devices filing the same pair while offline produce two identifiers for one
// record. The pair is what identifies it (C18).
func TestSyncResolvesAFilingByItsPair(t *testing.T) {
	node := serveSync(t)

	grouping := uuid.New().String()
	pair := map[string]any{"ebook_id": node.ebookID, "collection_id": grouping}

	node.push(t, node.change(t, quirev1.TargetEntity_TARGET_ENTITY_COLLECTION, grouping,
		quirev1.OperationKind_OPERATION_KIND_INSERT, 1, authored,
		map[string]any{"name": "Cabeceira", "kind": theShelf}))

	// Filed by one device, unfiled by another later, each naming a row
	// identifier of its own.
	filed := node.push(t, node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION,
		uuid.New().String(), quirev1.OperationKind_OPERATION_KIND_INSERT, 2,
		authored.Add(time.Second), pair))
	if filed.GetResults()[0].GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
		t.Fatalf("the filing was %s: %s", filed.GetResults()[0].GetOutcome(),
			filed.GetResults()[0].GetDetail())
	}

	cleared := node.push(t, node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK_COLLECTION,
		uuid.New().String(), quirev1.OperationKind_OPERATION_KIND_DELETE, 3,
		authored.Add(2*time.Second), pair))
	if cleared.GetResults()[0].GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
		t.Fatalf("the unfiling was %s: %s", cleared.GetResults()[0].GetOutcome(),
			cleared.GetResults()[0].GetDetail())
	}

	work := node.ebookID

	groupings, err := node.library.client.ListCollections(node.ctx,
		&quirev1.ListCollectionsRequest{EbookId: &work})
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}

	if len(groupings.GetCollections()) != 0 {
		t.Error("the second change wrote a second row instead of the one the pair already had")
	}
}

// The stream is the same push and pull kept open (UC10, UC11): it drains the
// backlog, stays open, and delivers what another call stores while it is.
func TestSyncStreamDrainsAndThenStaysOpen(t *testing.T) {
	node := serveSync(t)

	backlog := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
		quirev1.OperationKind_OPERATION_KIND_UPDATE, 2, authored,
		map[string]any{"title": "Os Sertões"})
	node.push(t, backlog)

	stream, err := node.client.Sync(node.ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if err = stream.Send(&quirev1.SyncRequest{
		Payload: &quirev1.SyncRequest_Start{Start: &quirev1.SyncStart{}},
	}); err != nil {
		t.Fatalf("sending the cursor: %v", err)
	}

	drained := receiveBatch(t, stream)
	if len(drained) != 1 || drained[0].GetId() != backlog.GetId() {
		t.Fatalf("the stream drained %d changes, want the backlog", len(drained))
	}

	if err = stream.Send(&quirev1.SyncRequest{
		Payload: &quirev1.SyncRequest_Ack{
			Ack: &quirev1.SyncAck{Position: drained[0].GetPosition()},
		},
	}); err != nil {
		t.Fatalf("acknowledging: %v", err)
	}

	// Stored by another call, on the same node, while the stream is open. The
	// hub is what carries it across.
	arriving := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
		quirev1.OperationKind_OPERATION_KIND_UPDATE, 3, authored.Add(time.Minute),
		map[string]any{"author": "Euclides da Cunha"})
	node.push(t, arriving)

	delivered := receiveBatch(t, stream)
	if len(delivered) != 1 || delivered[0].GetId() != arriving.GetId() {
		t.Fatalf("the stream delivered %d changes, want the one that arrived", len(delivered))
	}

	if err = stream.CloseSend(); err != nil {
		t.Fatalf("closing the stream: %v", err)
	}

	if _, err = stream.Recv(); err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("the stream ended with %v", err)
	}
}

// A push over the stream is the same push the unary call serves, answered with
// the verdicts.
func TestSyncStreamStoresWhatTheDevicePushes(t *testing.T) {
	node := serveSync(t)

	stream, err := node.client.Sync(node.ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if err = stream.Send(&quirev1.SyncRequest{
		Payload: &quirev1.SyncRequest_Start{Start: &quirev1.SyncStart{}},
	}); err != nil {
		t.Fatalf("sending the cursor: %v", err)
	}

	pushed := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
		quirev1.OperationKind_OPERATION_KIND_UPDATE, 2, authored,
		map[string]any{"title": "Os Sertões"})

	if err = stream.Send(&quirev1.SyncRequest{
		Payload: &quirev1.SyncRequest_Push{
			Push: &quirev1.OperationBatch{Operations: []*quirev1.Operation{pushed}},
		},
	}); err != nil {
		t.Fatalf("pushing over the stream: %v", err)
	}

	results := receiveResults(t, stream)
	if len(results) != 1 ||
		results[0].GetOutcome() != quirev1.OperationOutcome_OPERATION_OUTCOME_APPLIED {
		t.Fatalf("the stream answered %v", results)
	}

	_ = stream.CloseSend()

	if !holdsOperation(t, node.pull(t, 0), pushed.GetId()) {
		t.Error("the change the stream reported applied is not in the log")
	}
}

// The peer-facing call is authenticated by a certificate and not by a token, so
// a device holding a perfectly good session is still not a node.
func TestSyncRefusesAReplicationCallFromADevice(t *testing.T) {
	node := serveSync(t)

	_, err := node.client.ReplicateOperations(node.ctx, &quirev1.ReplicateOperationsRequest{
		UserId: node.userID,
	})

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("ReplicateOperations = %v, want a permission denied", err)
	}
}

// A peer carries no token — nobody ever issued it one — so the call has to
// reach the handler that reads its certificate.
//
// This is the regression the local federation of phase 10 found. The method was
// not in the identity slice's list of calls that need no access token, so the
// authentication interceptor refused every peer with Unauthenticated before the
// check that really identifies one could run, and the whole inbound half of the
// replication was unreachable in any node cmd/quired builds. What the test
// pins is not the refusal but where it comes from: this call is refused by the
// handler, over the certificate, and never by the interceptor over a token.
func TestSyncLetsACallerWithNoTokenReachTheReplicationHandler(t *testing.T) {
	node := serveSync(t)

	// The listener of this suite presents no certificate, so the handler
	// refuses the call for arriving over a connection that carries none —
	// which is the answer of the code that identifies peers, and the point.
	_, err := node.client.ReplicateOperations(t.Context(), &quirev1.ReplicateOperationsRequest{
		UserId: node.userID,
	})

	if status.Code(err) == codes.Unauthenticated {
		t.Fatalf("ReplicateOperations = %v, which is the interceptor refusing a peer for holding no token", err)
	}

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("ReplicateOperations = %v, want a permission denied from the handler", err)
	}
}

// holdsOperation reports whether a page carries the change.
func holdsOperation(t *testing.T, page *quirev1.PullOperationsResponse, id string) bool {
	t.Helper()

	for _, operation := range page.GetOperations() {
		if operation.GetId() == id {
			return true
		}
	}

	return false
}

// receiveBatch waits for the next page of changes the stream sends.
func receiveBatch(t *testing.T, stream quirev1.SyncService_SyncClient) []*quirev1.Operation {
	t.Helper()

	for {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("receiving from the stream: %v", err)
		}

		if batch := response.GetOperations(); batch != nil {
			return batch.GetOperations()
		}
	}
}

// receiveResults waits for the verdicts on a push over the stream.
func receiveResults(t *testing.T, stream quirev1.SyncService_SyncClient) []*quirev1.OperationResult {
	t.Helper()

	for {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("receiving from the stream: %v", err)
		}

		if pushed := response.GetPushResult(); pushed != nil {
			return pushed.GetResults()
		}
	}
}

// peers is a federation of one node that answers whatever the test set, so that
// the queue can be drained without a second node to drain it to.
//
// What these tests are about is the three statements the queue is made of —
// what fills it, what it hands out and in which order, and what closes a row —
// and none of that involves a peer. A real one belongs to the end-to-end suite,
// where two nodes exist.
type peers struct {
	mu sync.Mutex
	// refuse, when set, is what Replicate reports: the peer not answering,
	// which is the ordinary state of a node belonging to another operator.
	refuse error
	// silent, when set, is how many of the changes offered the destination
	// answers nothing about.
	silent int

	offered [][]uuid.UUID
}

// Replicate answers about what it was offered.
func (p *peers) Replicate(
	_ context.Context, _, _ uuid.UUID, operations []*operation.Operation,
) ([]operation.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	batch := make([]uuid.UUID, 0, len(operations))
	for _, op := range operations {
		batch = append(batch, op.ID)
	}

	p.offered = append(p.offered, batch)

	if p.refuse != nil {
		return nil, p.refuse
	}

	answered := max(len(operations)-p.silent, 0)
	results := make([]operation.Result, 0, answered)

	for _, op := range operations[:answered] {
		results = append(results, operation.Result{
			OperationID: op.ID,
			Verdict:     operation.Applied(),
		})
	}

	return results, nil
}

// Offered is every batch the federation was handed, in order.
func (p *peers) Offered() [][]uuid.UUID {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.offered)
}

// replication is one pass of the delivery queue over the real statements, and
// the peer it would have offered them to.
type replication struct {
	pass  *replicate.Replicate
	peers *peers
	// server is the node the reader authorized, which the queue is scoped by.
	server uuid.UUID
}

// authorizeReplica records a peer in the catalogue and the reader's permission
// for it, and returns the pass that drains what it is owed.
//
// The use case is assembled here rather than taken from the container, because
// the worker has no gRPC surface to reach it through: what the container hands
// back is a loop around a timer, and a test that waited for one would be
// testing the timer.
func (s *synchronization) authorizeReplica(t *testing.T, domain string) replication {
	t.Helper()

	manager := persist.NewManager(pool)
	catalogue := serverrepository.New(manager)
	authorizations := replicarepository.New(manager)

	peer, err := federationserver.New(&federationserver.Descriptor{
		Domain:                 federationserver.ParseDomain(domain),
		BaseURL:                federationserver.BaseURL("http://" + domain),
		CertificateFingerprint: federationserver.Fingerprint("spki-sha256:cGVlcg=="),
		GRPCAuthority:          federationserver.GRPCAuthority(domain + ":9090"),
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("describing the peer: %v", err)
	}

	if err = catalogue.Create(t.Context(), peer); err != nil {
		t.Fatalf("recording the peer: %v", err)
	}

	reader, err := uuid.Parse(s.userID)
	if err != nil {
		t.Fatalf("parsing the reader: %v", err)
	}

	granted, err := federationreplica.New(reader, peer.ID, false, time.Now().UTC())
	if err != nil {
		t.Fatalf("granting the authorization: %v", err)
	}

	if err = authorizations.Create(t.Context(), granted); err != nil {
		t.Fatalf("recording the authorization: %v", err)
	}

	federation := &peers{}

	return replication{
		pass: replicate.New(
			deliveryrepository.New(manager),
			operationrepository.New(manager),
			federation,
			syncclock.New(hlc.New()),
			30*time.Second,
			100,
		),
		peers:  federation,
		server: peer.ID,
	}
}

// run drains the queue once.
func (r replication) run(t *testing.T) replicate.Output {
	t.Helper()

	output, err := r.pass.Execute(t.Context(), replicate.Input{})
	if err != nil {
		t.Fatalf("the replication pass: %v", err)
	}

	return output
}

// The queue is filled from the log and not by the call that stored the change,
// which is what makes a peer authorized after the fact and a peer that missed a
// week the same case: both are simply owed what they have not been offered.
func TestSyncOwesANewlyAuthorizedPeerTheWholeLog(t *testing.T) {
	node := serveSync(t)

	for counter := range 3 {
		node.push(t, node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, uint64(counter)+2,
			authored.Add(time.Duration(counter)*time.Second),
			map[string]any{"title": "Os Sertões"}))
	}

	// Authorized only now, after every one of those changes was already
	// committed. A queue filled at ingest would owe this node nothing.
	replica := node.authorizeReplica(t, "quire-b.example")

	output := replica.run(t)

	switch {
	case output.Enqueued != 3:
		t.Errorf("the pass discovered %d changes owed, want the whole log", output.Enqueued)
	case output.Offered != 3:
		t.Errorf("the pass offered %d changes", output.Offered)
	case output.Confirmed != 3:
		t.Errorf("the pass confirmed %d", output.Confirmed)
	}

	batches := replica.peers.Offered()
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("the peer was offered %v", batches)
	}

	// A second pass owes it nothing: the rows are closed, and the watermark
	// stops the statement from finding them again.
	second := replica.run(t)
	if second.Enqueued != 0 || second.Offered != 0 {
		t.Errorf("the second pass reported %+v, want nothing left owed", second)
	}
}

// A peer is offered a reader's changes in the order this node committed them.
// The reconciler at the far end creates records only from an insert, so a batch
// carrying an update ahead of its insert would be refused there permanently.
func TestSyncOffersTheLogInTheOrderThisNodeCommittedIt(t *testing.T) {
	node := serveSync(t)

	replica := node.authorizeReplica(t, "quire-b.example")

	written := make([]string, 0, 4)

	for counter := range 4 {
		pushed := node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, uint64(counter)+2,
			authored.Add(time.Duration(counter)*time.Second),
			map[string]any{"title": "Os Sertões"})

		node.push(t, pushed)

		written = append(written, pushed.GetId())
	}

	replica.run(t)

	batches := replica.peers.Offered()
	if len(batches) != 1 {
		t.Fatalf("the peer was offered %d batches, want one per reader", len(batches))
	}

	for index, id := range batches[0] {
		if id.String() != written[index] {
			t.Errorf("change %d was offered out of the order the log holds", index)
		}
	}
}

// A peer belonging to another operator is unreachable often enough that
// retrying it at full rate would be this node's largest source of outbound
// traffic, so a try that did not land is counted and the row waits.
func TestSyncBacksOffAPeerThatDidNotAnswer(t *testing.T) {
	node := serveSync(t)

	node.push(t, node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
		quirev1.OperationKind_OPERATION_KIND_UPDATE, 2, authored,
		map[string]any{"title": "Os Sertões"}))

	replica := node.authorizeReplica(t, "quire-b.example")
	replica.peers.refuse = errs.New(errs.KindUnavailable, "no route to host")

	first := replica.run(t)
	if first.Failed != 1 || first.Confirmed != 0 {
		t.Fatalf("the pass reported %+v, want the change left owed", first)
	}

	// The backoff is in the statement, so the second pass at the same instant
	// finds the row waiting rather than reading it in order to skip it.
	second := replica.run(t)
	if second.Offered != 0 {
		t.Errorf("the pass offered %d changes, want the backoff to have held them", second.Offered)
	}

	if offered := replica.peers.Offered(); len(offered) != 1 {
		t.Errorf("the peer was dialed %d times, want the backoff to have held the second", len(offered))
	}
}

// What is retried is what the destination did not answer for, which is a call
// that was cut short rather than a change that was refused.
func TestSyncLeavesOwedWhatThePeerDidNotAnswerFor(t *testing.T) {
	node := serveSync(t)

	for counter := range 3 {
		node.push(t, node.change(t, quirev1.TargetEntity_TARGET_ENTITY_EBOOK, node.ebookID,
			quirev1.OperationKind_OPERATION_KIND_UPDATE, uint64(counter)+2,
			authored.Add(time.Duration(counter)*time.Second),
			map[string]any{"title": "Os Sertões"}))
	}

	replica := node.authorizeReplica(t, "quire-b.example")
	replica.peers.silent = 2

	output := replica.run(t)
	if output.Confirmed != 1 || output.Failed != 2 {
		t.Errorf("the pass reported %+v, want one settled and two left owed", output)
	}
}
