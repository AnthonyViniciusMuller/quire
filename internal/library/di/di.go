// Package di builds the library slice: it constructs every adapter, wires them
// into the use cases, wires those into the controllers, and hands back what the
// node needs from this slice.
//
// It is the only place where a concrete adapter of this slice is named. That
// matters more here than in the slices before it, because one of the ports has
// three implementations: which object store holds the reader's files is decided
// in this file and nowhere else, and every layer above it holds
// service.BlobStore.
//
// The provider is not read from a variable that names it. It is inferred from
// which section of the configuration the deployment filled in, so a name and
// the credentials beside it cannot disagree; the configuration has already
// refused to load if none or more than one was.
//
// It reads no environment variable and opens no connection. The configuration
// arrives loaded and the pool arrives open, because both are shared with the
// slices around it and neither is this slice's to decide.
package di

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	addtocollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/addtocollection"
	createcollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/createcollection"
	createebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/createebook"
	deletecollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deletecollection"
	deleteebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/deleteebook"
	downloadcontentusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/downloadcontent"
	getcollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/getcollection"
	getebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/getebook"
	listcollectionsusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/listcollections"
	listebooksusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/listebooks"
	removefromcollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/removefromcollection"
	updatecollectionusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/updatecollection"
	updateebookusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/updateebook"
	uploadcontentusecase "github.com/anthonyvsmuller/quire/internal/library/application/usecase/uploadcontent"
	"github.com/anthonyvsmuller/quire/internal/library/domain/collection"
	"github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	"github.com/anthonyvsmuller/quire/internal/library/domain/membership"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/addebooktocollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/createcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/createebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/deletecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/deleteebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/downloadebookcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/getcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/getebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/listcollections"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/listebooks"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/removeebookfromcollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/updatecollection"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/updateebook"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/controller/uploadebookcontent"
	"github.com/anthonyvsmuller/quire/internal/library/infra/grpc/libraryservice"
	collectionrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/collection"
	contentrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/content"
	ebookrepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/ebook"
	membershiprepository "github.com/anthonyvsmuller/quire/internal/library/infra/repository/membership"
	clockservice "github.com/anthonyvsmuller/quire/internal/library/infra/service/clock"
	gcsservice "github.com/anthonyvsmuller/quire/internal/library/infra/service/gcs"
	minioservice "github.com/anthonyvsmuller/quire/internal/library/infra/service/minio"
	s3service "github.com/anthonyvsmuller/quire/internal/library/infra/service/s3"
	stagingservice "github.com/anthonyvsmuller/quire/internal/library/infra/service/staging"
	uploadsservice "github.com/anthonyvsmuller/quire/internal/library/infra/service/uploads"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// opInitialize is the operation reported by this file, in the form the errs
// package expects.
const opInitialize = "library/di: initialize"

// Container is what the node takes from this slice.
type Container struct {
	// Service is the gRPC surface of the slice, ready to be registered.
	Service *libraryservice.Service

	// Ebooks is the collection this node holds. The reading slice reaches it
	// through its own port, which is what establishes whose a mark or a
	// reading position is: both of its tables reference a work and neither
	// references a reader.
	Ebooks ebook.Repository

	// Collections and Memberships are the rest of what replicates through this
	// slice. The sync slice's reconciler writes all three, and it reaches each
	// of them through the port its own slice declares rather than through a
	// statement of its own — which is what keeps the tombstone rule, the
	// ownership check and the stamping in the package that has them.
	Collections collection.Repository
	Memberships membership.Repository

	// Uploads ends the chunked uploads nobody is sending to any more. It is not
	// a server and nobody calls it, so the node runs it beside the two
	// listeners — a half-received file that outlived the reader who abandoned
	// it is disk this node never gets back on its own.
	Uploads *uploadsservice.Service

	// closer releases what the object store client holds, when it holds
	// anything. Only one of the three adapters does.
	closer func() error
}

// Close releases what the slice holds. The node defers it.
func (c *Container) Close() error {
	if c.closer == nil {
		return nil
	}

	return c.closer()
}

// Initialize builds the slice over the node's configuration and connection
// pool.
//
// It can fail, and the only thing that can fail is the object store: a MinIO
// endpoint the SDK cannot address, or Google credentials that cannot be read.
// Both are deployment faults, and both stop the node while it is starting
// rather than at the first import — which is the right way round, because a
// node that cannot store a file cannot serve UC02 at all and should say so
// before it starts answering.
//
// Nothing is dialled. A store that is down is a failed call and not a node
// that refuses to start: this slice also serves metadata for readers whose
// files it does not hold, and it should keep serving it.
func Initialize(
	ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, stamps *hlc.Clock, logger *slog.Logger,
) (*Container, error) {
	manager := persist.NewManager(pool)

	works := ebookrepository.New(manager)
	collections := collectionrepository.New(manager)
	memberships := membershiprepository.New(manager)
	contents := contentrepository.New(manager)

	blobs, closer, err := blobStore(ctx, &cfg.Storage)
	if err != nil {
		return nil, err
	}

	staging := stagingservice.New()
	uploads := uploadsservice.New(staging, cfg.Storage.UploadExpiry, cfg.Storage.MaxUploadBytes, logger)
	clock := clockservice.New(stamps)

	// The manager itself is the unit of work: its Within is the port, so no
	// adapter stands between them.
	transaction := manager

	controllers := libraryservice.Controllers{
		CreateEbook: createebook.New(createebookusecase.New(works, contents, clock)),
		GetEbook:    getebook.New(getebookusecase.New(works)),
		ListEbooks:  listebooks.New(listebooksusecase.New(works)),
		UpdateEbook: updateebook.New(updateebookusecase.New(works, clock)),
		DeleteEbook: deleteebook.New(
			deleteebookusecase.New(works, memberships, clock, transaction)),
		UploadEbookContent: uploadebookcontent.New(
			uploadcontentusecase.New(works, contents, blobs, staging, clock, cfg.Storage.MaxUploadBytes)),
		DownloadEbookContent: downloadebookcontent.New(
			downloadcontentusecase.New(works, contents, blobs)),
		CreateCollection: createcollection.New(createcollectionusecase.New(collections, clock)),
		GetCollection:    getcollection.New(getcollectionusecase.New(collections)),
		ListCollections:  listcollections.New(listcollectionsusecase.New(collections)),
		UpdateCollection: updatecollection.New(updatecollectionusecase.New(collections, clock)),
		DeleteCollection: deletecollection.New(
			deletecollectionusecase.New(collections, memberships, clock, transaction)),
		AddEbookToCollection: addebooktocollection.New(
			addtocollectionusecase.New(works, collections, memberships, clock, transaction)),
		RemoveEbookFromCollection: removeebookfromcollection.New(
			removefromcollectionusecase.New(works, collections, memberships, clock, transaction)),
	}

	return &Container{
		Service:     libraryservice.New(&controllers),
		Ebooks:      works,
		Collections: collections,
		Memberships: memberships,
		Uploads:     uploads,
		closer:      closer,
	}, nil
}

// blobStore builds the adapter the configuration named, and returns what
// releases it — nil for the two that hold nothing.
//
// The default case is unreachable: config.Validate has already refused a
// deployment that named none or more than one. It is here because the compiler
// asks, and because "unreachable" is a claim about another file that should
// fail loudly if it ever stops being true.
func blobStore(
	ctx context.Context, cfg *config.Storage,
) (service.BlobStore, func() error, error) {
	switch cfg.Provider() {
	case config.StorageProviderS3:
		return s3service.New(cfg), nil, nil

	case config.StorageProviderMinIO:
		store, err := minioservice.New(cfg)

		return store, nil, err

	case config.StorageProviderGCS:
		store, err := gcsservice.New(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}

		return store, store.Close, nil

	case config.StorageProviderNone:
		return nil, nil, noStore()

	default:
		return nil, nil, noStore()
	}
}

// noStore is the answer to a configuration that named no object store, which
// config.Validate has already refused.
func noStore() error {
	return errs.New(errs.KindFailedPrecondition, "the node has nowhere to put a file").
		WithOp(opInitialize).
		WithField("QUIRE_STORAGE_*", "exactly one object store section must be configured")
}
