// Package di builds the reading slice: it constructs every adapter, wires them
// into the use cases, wires those into the controllers, and hands back what the
// node needs from this slice.
//
// It is the only place where a concrete adapter of this slice is named. There
// are two, and one of them is not this slice's to build: whether a reader may
// write in a work is a question about library.ebooks, so Initialize takes the
// library's works repository and wraps it in the adapter of the Works port.
// That is the shape the identity slice has over the federation catalogue, and
// it is wired the same way — in cmd/quired, where the containers meet, so that
// neither slice imports the other's di.
//
// It reads no environment variable and opens no connection. The configuration
// arrives loaded and the pool arrives open, because both are shared with the
// slices around it and neither is this slice's to decide.
//
// Nothing here can fail. This slice holds no secret, reaches no peer and
// chooses no provider — unlike the identity slice, which reads a signing key,
// and the library slice, which decides which object store holds the files. A
// container that cannot fail is a container the node can build without
// deciding what to do about it.
package di

import (
	"github.com/jackc/pgx/v5/pgxpool"

	libraryebook "github.com/anthonyvsmuller/quire/internal/library/domain/ebook"
	createannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/createannotation"
	deleteannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/deleteannotation"
	getannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/getannotation"
	listannotationsusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listannotations"
	listprogressusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/listprogress"
	updateannotationusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateannotation"
	updateprogressusecase "github.com/anthonyvsmuller/quire/internal/reading/application/usecase/updateprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/createannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/deleteannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/getannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/listannotations"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/listreadingprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/updateannotation"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/controller/updatereadingprogress"
	"github.com/anthonyvsmuller/quire/internal/reading/infra/grpc/readingservice"
	annotationrepository "github.com/anthonyvsmuller/quire/internal/reading/infra/repository/annotation"
	progressrepository "github.com/anthonyvsmuller/quire/internal/reading/infra/repository/progress"
	clockservice "github.com/anthonyvsmuller/quire/internal/reading/infra/service/clock"
	worksservice "github.com/anthonyvsmuller/quire/internal/reading/infra/service/works"
	"github.com/anthonyvsmuller/quire/internal/shared/hlc"
	"github.com/anthonyvsmuller/quire/internal/shared/persist"
)

// Container is what the node takes from this slice.
type Container struct {
	// Service is the gRPC surface of the slice, ready to be registered.
	Service *readingservice.Service
}

// Initialize builds the slice over the node's connection pool and the library
// slice's works repository.
func Initialize(pool *pgxpool.Pool, library libraryebook.Repository, stamps *hlc.Clock) *Container {
	manager := persist.NewManager(pool)

	marks := annotationrepository.New(manager)
	positions := progressrepository.New(manager)

	works := worksservice.New(library)
	clock := clockservice.New(stamps)

	controllers := readingservice.Controllers{
		CreateAnnotation: createannotation.New(
			createannotationusecase.New(marks, works, clock)),
		GetAnnotation:   getannotation.New(getannotationusecase.New(marks, works)),
		ListAnnotations: listannotations.New(listannotationsusecase.New(marks, works)),
		UpdateAnnotation: updateannotation.New(
			updateannotationusecase.New(marks, works, clock)),
		DeleteAnnotation: deleteannotation.New(
			deleteannotationusecase.New(marks, works, clock)),
		UpdateReadingProgress: updatereadingprogress.New(
			updateprogressusecase.New(positions, works, clock)),
		ListReadingProgress: listreadingprogress.New(
			listprogressusecase.New(positions, works)),
	}

	return &Container{Service: readingservice.New(&controllers)}
}
