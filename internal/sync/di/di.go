// Package di builds the sync slice: it constructs every adapter, wires them
// into the use cases, wires those into the controllers, and hands back what the
// node needs from this slice.
//
// It is the only place where a concrete adapter of this slice is named, and the
// slice with the most of somebody else's: the reconciler writes the records
// five repositories in two other slices own, so Initialize takes them and wraps
// them behind the one port the use cases hold. That is the shape
// internal/reading/infra/service/works set, and it is wired the same way — in
// cmd/quired, where the containers meet, so that no slice imports another's di.
//
// It reads no environment variable and opens no connection. The configuration
// arrives loaded and the pool arrives open, because both are shared with the
// slices around it and neither is this slice's to decide. The node's clock
// arrives the same way, and for a reason of its own: a second hybrid logical
// clock in this process would be a second answer to what "after" means here.
//
// Nothing here can fail. This slice holds no secret, chooses no provider and,
// so far, reaches no peer — the outbound half of replication arrives with the
// worker, and it is the first thing in this container that will be able to.
package di

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	librarycollection "github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	librarymembership "github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	readingannotation "github.com/anthonyvsmuller/quire/internal/reading/domain/annotation"
	readingprogress "github.com/anthonyvsmuller/quire/internal/reading/domain/progress"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
	pulloperationsusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pulloperations"
	pushoperationsusecase "github.com/anthonyvsmuller/quire/internal/sync/application/usecase/pushoperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/pulloperations"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/pushoperations"
	syncstream "github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/controller/sync"
	"github.com/anthonyvsmuller/quire/internal/sync/infra/grpc/syncservice"
	operationrepository "github.com/anthonyvsmuller/quire/internal/sync/infra/repository/operation"
	changesservice "github.com/anthonyvsmuller/quire/internal/sync/infra/service/changes"
	clockservice "github.com/anthonyvsmuller/quire/internal/sync/infra/service/clock"
	recordsservice "github.com/anthonyvsmuller/quire/internal/sync/infra/service/records"
)

// streamPoll is how often an open stream looks for changes nobody told it
// about.
//
// The hub is in-process, so two devices whose streams are open against two
// replicas of this node do not wake each other. This is the interval at which
// they find out anyway, and it is short enough that a reader notices a delay
// and long enough that a thousand idle streams are not a thousand queries a
// second.
const streamPoll = 5 * time.Second

// Records is what the reconciler reaches the replicated records through, one
// per table the node replicates.
//
// It is this slice's own name for the five repositories the library and reading
// slices own, so that cmd/quired hands them over in one value and the wiring
// says which is which.
type Records struct {
	// Works is library.ebooks.
	Works libraryebook.Repository
	// Groupings is library.collections.
	Groupings librarycollection.Repository
	// Filings is library.ebook_collections.
	Filings librarymembership.Repository
	// Marks is reading.annotations.
	Marks readingannotation.Repository
	// Positions is reading.progress.
	Positions readingprogress.Repository
}

// Container is what the node takes from this slice.
type Container struct {
	// Service is the gRPC surface of the slice, ready to be registered.
	Service *syncservice.Service
}

// Initialize builds the slice over the node's connection pool, its clock and
// the repositories of the records that replicate through it.
func Initialize(pool *pgxpool.Pool, stamps *hlc.Clock, records *Records) *Container {
	manager := persist.NewManager(pool)

	log := operationrepository.New(manager)

	clock := clockservice.New(stamps)
	hub := changesservice.New()
	reconciler := recordsservice.New(&recordsservice.Repositories{
		Works:     records.Works,
		Groupings: records.Groupings,
		Filings:   records.Filings,
		Marks:     records.Marks,
		Positions: records.Positions,
	})

	push := pushoperationsusecase.New(log, reconciler, clock, manager, hub)
	pull := pulloperationsusecase.New(log)

	controllers := syncservice.Controllers{
		PushOperations: pushoperations.New(push),
		PullOperations: pulloperations.New(pull),
		Sync:           syncstream.New(push, pull, hub, streamPoll),
	}

	return &Container{Service: syncservice.New(&controllers)}
}
