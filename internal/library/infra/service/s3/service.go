// Package s3 stores e-book files in Amazon S3, through the AWS SDK.
//
// It is one of three adapters of service.BlobStore, and the node builds
// whichever one the configuration named. What separates it from the MinIO
// adapter beside it is not the protocol — they speak the same one — but which
// client speaks it: this one is the SDK Amazon publishes, addressed by region,
// and it is the adapter a deployment against S3 itself should use.
//
// It carries no credential chain. The SDK's own — an instance role, a service
// account with IRSA — lives in modules this node does not depend on, so the
// key pair is configuration and the configuration refuses to load without it.
package s3

import (
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/library/domain/content"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// The operations reported by this file, in the form the errs package expects.
const (
	opPut    = "library/s3: put"
	opOpen   = "library/s3: open"
	opRemove = "library/s3: remove"
)

// credentialSource is what the SDK records as the origin of the key pair, and
// what appears in its own diagnostics when a request is refused.
//
//nolint:gosec // G101: this is the name of a source of credentials, not one.
const credentialSource = "quire configuration"

// Service stores objects in S3.
type Service struct {
	client *awss3.Client
	bucket string
}

// Service satisfies the port the use cases hold.
var _ service.BlobStore = (*Service)(nil)

// New returns a store over the S3 section of the configuration.
//
// Nothing is dialled here. The SDK builds a client without contacting
// anything, so a bucket that does not exist is a failed call and not a node
// that refuses to start — which is the right way round: the node serves
// metadata for readers whose files it does not hold, and it should keep
// serving it when the object store is down.
func New(cfg *config.Storage) *Service {
	options := awss3.Options{
		Region: cfg.S3.Region,
		Credentials: staticCredentials{
			key:    cfg.S3.AccessKeyID,
			secret: string(cfg.S3.SecretAccessKey),
		},
	}

	// An endpoint is given only for the S3-compatible services that are
	// neither Amazon nor MinIO, and those almost always address buckets as a
	// path segment rather than as a subdomain — a bucket subdomain of an
	// arbitrary host is a certificate nobody issued.
	if cfg.S3.Endpoint != "" {
		options.BaseEndpoint = aws.String(cfg.S3.Endpoint)
		options.UsePathStyle = true
	}

	return &Service{client: awss3.New(options), bucket: cfg.Bucket}
}

// Bucket names where this store puts things.
func (s *Service) Bucket() string { return s.bucket }

// Put stores the bytes and returns where they went.
func (s *Service) Put(
	ctx context.Context, blob *service.Blob, body io.Reader,
) (content.Locator, error) {
	at := content.Locator{Bucket: s.bucket, Key: service.ObjectKey(blob.Hash)}

	// The length is declared rather than discovered. Without it the SDK reads
	// the whole file into memory to find it, which for a work a reader chose
	// is a size this node does not get to bound.
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:        aws.String(at.Bucket),
		Key:           aws.String(at.Key),
		Body:          body,
		ContentLength: aws.Int64(blob.Size),
		ContentType:   aws.String(blob.MediaType.String()),
	})
	if err != nil {
		return content.Locator{}, classify(err, opPut)
	}

	return at, nil
}

// Open reads the bytes back. The caller closes what it returns.
func (s *Service) Open(ctx context.Context, at content.Locator) (io.ReadCloser, error) {
	output, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(at.Bucket),
		Key:    aws.String(at.Key),
	})
	if err != nil {
		return nil, classify(err, opOpen)
	}

	return output.Body, nil
}

// Remove deletes the object.
func (s *Service) Remove(ctx context.Context, at content.Locator) error {
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(at.Bucket),
		Key:    aws.String(at.Key),
	})

	return classify(err, opRemove)
}

// classify translates an SDK error into the vocabulary of the node.
//
// The missing-object codes are matched through smithy.APIError rather than
// through the typed errors of the S3 package, because the two operations that
// can raise one raise different types for the same condition: a read reports
// NoSuchKey and a head reports NotFound, and the caller cares about neither
// distinction.
func classify(err error, op string) error {
	if err == nil {
		return nil
	}

	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return errs.Wrap(err, errs.KindNotFound, "the object store does not have that file").
				WithOp(op).
				WithCode(service.CodeBlobNotFound)
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return errs.Wrap(err, errs.KindPermissionDenied, "the object store refused this node").
				WithOp(op).
				WithCode(service.CodeBlobUnavailable)
		}
	}

	return errs.Wrap(err, errs.KindUnavailable, "the object store is not answering").
		WithOp(op).
		WithCode(service.CodeBlobUnavailable)
}

// staticCredentials hands the SDK the key pair the configuration carries.
//
// It is four lines here rather than a call to
// aws-sdk-go-v2/credentials.NewStaticCredentialsProvider, because that module
// exists to reach STS and SSO for the providers this node does not use, and
// depending on it would pull both in for a struct with two fields.
type staticCredentials struct {
	key    string
	secret string
}

// Retrieve answers with the configured key pair.
func (s staticCredentials) Retrieve(_ context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:     s.key,
		SecretAccessKey: s.secret,
		Source:          credentialSource,
	}, nil
}
