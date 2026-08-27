// Package minio stores e-book files in a self-hosted MinIO, through the MinIO
// SDK.
//
// It is one of three adapters of service.BlobStore. MinIO speaks the S3 API,
// so the adapter beside it would serve a MinIO too, and this one exists for
// two reasons that are not protocol: it is addressed by an endpoint rather
// than by a region, which is how a self-hosted store is reached, and it is the
// client MinIO publishes, so a deployment against MinIO is running the code
// its operator's own documentation describes.
package minio

import (
	"context"
	"errors"
	"io"
	"net/http"

	miniogo "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew    = "library/minio: new"
	opPut    = "library/minio: put"
	opOpen   = "library/minio: open"
	opRemove = "library/minio: remove"
)

// Service stores objects in MinIO.
type Service struct {
	client *miniogo.Client
	bucket string
}

// Service satisfies the port the use cases hold.
var _ service.BlobStore = (*Service)(nil)

// New returns a store over the MinIO section of the configuration.
//
// It can fail, which the S3 adapter cannot, because the SDK parses the
// endpoint here rather than at the first call. Nothing is dialled: a MinIO
// that is down is a failed call and not a node that refuses to start.
func New(cfg *config.Storage) (*Service, error) {
	client, err := miniogo.New(cfg.MinIO.Endpoint, &miniogo.Options{
		Creds: credentials.NewStaticV4(
			cfg.MinIO.AccessKeyID, string(cfg.MinIO.SecretAccessKey), ""),
		Secure: cfg.MinIO.UseTLS,
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalidArgument, "the object store could not be addressed").
			WithOp(opNew).
			WithField("QUIRE_STORAGE_MINIO_ENDPOINT", "it must be a host and port MinIO answers on")
	}

	return &Service{client: client, bucket: cfg.Bucket}, nil
}

// Bucket names where this store puts things.
func (s *Service) Bucket() string { return s.bucket }

// Put stores the bytes and returns where they went.
func (s *Service) Put(
	ctx context.Context, blob *service.Blob, body io.Reader,
) (content.Locator, error) {
	at := content.Locator{Bucket: s.bucket, Key: service.ObjectKey(blob.Hash)}

	// The length is declared rather than passed as -1. A negative length makes
	// the SDK buffer the whole file in order to find one, which for a work a
	// reader chose is a size this node does not get to bound.
	_, err := s.client.PutObject(ctx, at.Bucket, at.Key, body, blob.Size,
		miniogo.PutObjectOptions{ContentType: blob.MediaType.String()})
	if err != nil {
		return content.Locator{}, classify(err, opPut)
	}

	return at, nil
}

// Open reads the bytes back. The caller closes what it returns.
//
// The Stat is not a second round trip for its own sake. The SDK's GetObject is
// lazy — it returns an object whose first Read performs the request — so
// without it a file this node does not have would be reported not as a missing
// object but as a stream that ended immediately, halfway through a reply the
// client had already started receiving.
func (s *Service) Open(ctx context.Context, at content.Locator) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, at.Bucket, at.Key, miniogo.GetObjectOptions{})
	if err != nil {
		return nil, classify(err, opOpen)
	}

	if _, err := object.Stat(); err != nil {
		_ = object.Close()

		return nil, classify(err, opOpen)
	}

	return object, nil
}

// Remove deletes the object.
func (s *Service) Remove(ctx context.Context, at content.Locator) error {
	err := s.client.RemoveObject(ctx, at.Bucket, at.Key, miniogo.RemoveObjectOptions{})

	return classify(err, opRemove)
}

// classify translates an SDK error into the vocabulary of the node.
//
// The status code is read rather than the error code, because the two
// conditions worth telling apart — the object is not there, and this node may
// not have it — are exactly the two the transport already distinguishes, while
// the error strings differ between MinIO and the S3 implementations it is
// compatible with.
func classify(err error, op string) error {
	if err == nil {
		return nil
	}

	var already *errs.Error
	if errors.As(err, &already) {
		return err
	}

	switch miniogo.ToErrorResponse(err).StatusCode {
	case http.StatusNotFound:
		return errs.Wrap(err, errs.KindNotFound, "the object store does not have that file").
			WithOp(op).
			WithCode(service.CodeBlobNotFound)
	case http.StatusForbidden, http.StatusUnauthorized:
		return errs.Wrap(err, errs.KindPermissionDenied, "the object store refused this node").
			WithOp(op).
			WithCode(service.CodeBlobUnavailable)
	}

	return errs.Wrap(err, errs.KindUnavailable, "the object store is not answering").
		WithOp(op).
		WithCode(service.CodeBlobUnavailable)
}
