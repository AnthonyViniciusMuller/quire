// Package gcs stores e-book files in Google Cloud Storage, through the Cloud
// Storage SDK.
//
// It is the one of the three adapters that speaks a different protocol. S3 and
// MinIO share an API and differ in how they are addressed; this one is the
// JSON API with OAuth 2.0 credentials, which is what Google documents as the
// way in and the only one that accepts Workload Identity on GKE.
//
// It is also the only adapter whose credentials may be left out of the
// configuration, and not because it is privileged: application default
// credentials are built into the SDK the node already depends on, so honouring
// them costs nothing here — which is exactly what the equivalent for S3 does
// not.
package gcs

import (
	"context"
	"errors"
	"io"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opNew    = "library/gcs: new"
	opPut    = "library/gcs: put"
	opOpen   = "library/gcs: open"
	opRemove = "library/gcs: remove"
	opClose  = "library/gcs: close"
)

// Service stores objects in Cloud Storage.
type Service struct {
	client *storage.Client
	bucket string
}

// Service satisfies the port the use cases hold.
var _ service.BlobStore = (*Service)(nil)

// New returns a store over the GCS section of the configuration.
//
// The context is the node's startup context and is used to discover
// credentials, which is the one thing this constructor does that reaches
// outside the process — a metadata server on GKE, a file on a developer's
// machine. It is deliberately at startup: a node whose service account is
// wrong should say so while it is starting rather than at the first import.
func New(ctx context.Context, cfg *config.Storage) (*Service, error) {
	refuse := func(err error) error {
		return errs.Wrap(err, errs.KindFailedPrecondition,
			"the object store credentials could not be read").
			WithOp(opNew).
			WithField("QUIRE_STORAGE_GCS_CREDENTIALS_FILE",
				"it must be a service account key, or be left empty for application default credentials")
	}

	// Both paths narrow the scope to what this node does with the bucket. A
	// node that can only read and write objects is a node whose leaked
	// credential cannot delete the project.
	options := &credentials.DetectOptions{Scopes: []string{storage.ScopeReadWrite}}

	// A named file is loaded as a service account and nothing else. The SDK
	// deprecated the general "read whatever is in this file" option, and the
	// reason applies here even though the path is an operator's: a credential
	// configuration can name an external source to fetch a token from, so a
	// file that turned out to be one would make this node authenticate through
	// somewhere nobody chose.
	//
	// An empty path is the application default chain, which is the Workload
	// Identity token on GKE and what `gcloud auth` leaves on a developer's
	// machine.
	var (
		detected *auth.Credentials
		err      error
	)

	if cfg.GCS.CredentialsFile != "" {
		detected, err = credentials.NewCredentialsFromFile(
			credentials.ServiceAccount, cfg.GCS.CredentialsFile, options)
	} else {
		detected, err = credentials.DetectDefault(options)
	}

	if err != nil {
		return nil, refuse(err)
	}

	client, err := storage.NewClient(ctx, option.WithAuthCredentials(detected))
	if err != nil {
		return nil, refuse(err)
	}

	return &Service{client: client, bucket: cfg.Bucket}, nil
}

// Bucket names where this store puts things.
func (s *Service) Bucket() string { return s.bucket }

// Close releases the SDK's connections. The node's container calls it on the
// way down; the other two adapters need no equivalent, which is why the port
// does not declare one.
func (s *Service) Close() error {
	if err := s.client.Close(); err != nil {
		return errs.Wrap(err, errs.KindInternal, "the object store client could not be closed").WithOp(opClose)
	}

	return nil
}

// Put stores the bytes and returns where they went.
//
// The declared length is not passed to the writer, because this SDK does not
// take one: it uploads in chunks and discovers the length as it goes. What the
// length is used for here is the same thing it is used for everywhere else —
// the caller checked it against what arrived before calling this.
func (s *Service) Put(
	ctx context.Context, blob *service.Blob, body io.Reader,
) (content.Locator, error) {
	at := content.Locator{Bucket: s.bucket, Key: service.ObjectKey(blob.Hash)}

	writer := s.client.Bucket(at.Bucket).Object(at.Key).NewWriter(ctx)
	writer.ContentType = blob.MediaType.String()

	if _, err := io.Copy(writer, body); err != nil {
		// The object is only created when the writer closes cleanly, so
		// abandoning it here leaves nothing behind to remove.
		_ = writer.Close()

		return content.Locator{}, classify(err, opPut)
	}

	if err := writer.Close(); err != nil {
		return content.Locator{}, classify(err, opPut)
	}

	return at, nil
}

// Open reads the bytes back. The caller closes what it returns.
func (s *Service) Open(ctx context.Context, at content.Locator) (io.ReadCloser, error) {
	reader, err := s.client.Bucket(at.Bucket).Object(at.Key).NewReader(ctx)
	if err != nil {
		return nil, classify(err, opOpen)
	}

	return reader, nil
}

// Remove deletes the object.
func (s *Service) Remove(ctx context.Context, at content.Locator) error {
	return classify(s.client.Bucket(at.Bucket).Object(at.Key).Delete(ctx), opRemove)
}

// classify translates an SDK error into the vocabulary of the node.
func classify(err error, op string) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, storage.ErrObjectNotExist) || errors.Is(err, storage.ErrBucketNotExist) {
		return errs.Wrap(err, errs.KindNotFound, "the object store does not have that file").
			WithOp(op).
			WithCode(service.CodeBlobNotFound)
	}

	return errs.Wrap(err, errs.KindUnavailable, "the object store is not answering").
		WithOp(op).
		WithCode(service.CodeBlobUnavailable)
}
