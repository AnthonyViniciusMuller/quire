package s3

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// storageConfig is a node pointed at S3.
func storageConfig() *config.Storage {
	return &config.Storage{
		Bucket: "quire-contents",
		S3: config.StorageS3{
			Region:          "sa-east-1",
			AccessKeyID:     "AKIA",
			SecretAccessKey: "secret",
		},
	}
}

func TestNewReportsTheBucketItWasPointedAt(t *testing.T) {
	t.Parallel()

	if bucket := New(storageConfig()).Bucket(); bucket != "quire-contents" {
		t.Errorf("Bucket() = %q", bucket)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		code string
		kind errs.Kind
	}{
		"a read of an object that is not there": {"NoSuchKey", errs.KindNotFound},
		"a head of one":                         {"NotFound", errs.KindNotFound},
		"the wrong bucket":                      {"NoSuchBucket", errs.KindNotFound},
		"credentials the store refuses":         {"SignatureDoesNotMatch", errs.KindPermissionDenied},
		"anything else":                         {"InternalError", errs.KindUnavailable},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := classify(&smithy.GenericAPIError{Code: testCase.code}, opOpen)

			if !errors.Is(err, testCase.kind) {
				t.Errorf("classify(%s) is not %v: %v", testCase.code, testCase.kind, err)
			}
		})
	}
}

// A read of a missing object has to be told apart from a store that is down,
// because only the first is something the caller can answer a client with.
func TestClassifyNamesTheCodeTheCallerActsOn(t *testing.T) {
	t.Parallel()

	missing := classify(&smithy.GenericAPIError{Code: "NoSuchKey"}, opOpen)
	if errs.CodeOf(missing) != service.CodeBlobNotFound {
		t.Errorf("a missing object was reported as %q", errs.CodeOf(missing))
	}

	down := classify(errors.New("dial tcp: connection refused"), opOpen)
	if errs.CodeOf(down) != service.CodeBlobUnavailable {
		t.Errorf("a store that is down was reported as %q", errs.CodeOf(down))
	}
}

func TestClassifyPassesNilThrough(t *testing.T) {
	t.Parallel()

	if err := classify(nil, opPut); err != nil {
		t.Errorf("classify(nil) = %v", err)
	}
}

func TestStaticCredentialsAnswerWithWhatWasConfigured(t *testing.T) {
	t.Parallel()

	value, err := staticCredentials{key: "AKIA", secret: "secret"}.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if value.AccessKeyID != "AKIA" || value.SecretAccessKey != "secret" {
		t.Error("the SDK was handed credentials other than the configured ones")
	}
}
