package minio

import (
	"errors"
	"net/http"
	"testing"

	miniogo "github.com/minio/minio-go/v7"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// storageConfig is a node pointed at a MinIO beside it.
func storageConfig() *config.Storage {
	return &config.Storage{
		Bucket: "quire-contents",
		MinIO: config.StorageMinIO{
			Endpoint:        "localhost:9000",
			AccessKeyID:     "quire",
			SecretAccessKey: "quire-secret",
		},
	}
}

func TestNewReportsTheBucketItWasPointedAt(t *testing.T) {
	t.Parallel()

	store, err := New(storageConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if store.Bucket() != "quire-contents" {
		t.Errorf("Bucket() = %q", store.Bucket())
	}
}

// The SDK parses the endpoint at construction, so a malformed one is caught
// while the node is starting rather than at the first import.
func TestNewRefusesAnEndpointTheSDKCannotAddress(t *testing.T) {
	t.Parallel()

	cfg := storageConfig()
	cfg.MinIO.Endpoint = "http://localhost:9000/path"

	if _, err := New(cfg); !errors.Is(err, errs.KindInvalidArgument) {
		t.Errorf("New = %v, want an invalid argument", err)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		kind   errs.Kind
	}{
		"an object that is not there":   {http.StatusNotFound, errs.KindNotFound},
		"credentials the store refuses": {http.StatusForbidden, errs.KindPermissionDenied},
		"a store that is unwell":        {http.StatusInternalServerError, errs.KindUnavailable},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := classify(miniogo.ErrorResponse{StatusCode: testCase.status}, opOpen)

			if !errors.Is(err, testCase.kind) {
				t.Errorf("classify(%d) is not %v: %v", testCase.status, testCase.kind, err)
			}
		})
	}
}

func TestClassifyNamesTheCodeTheCallerActsOn(t *testing.T) {
	t.Parallel()

	missing := classify(miniogo.ErrorResponse{StatusCode: http.StatusNotFound}, opOpen)
	if errs.CodeOf(missing) != service.CodeBlobNotFound {
		t.Errorf("a missing object was reported as %q", errs.CodeOf(missing))
	}

	down := classify(errors.New("dial tcp: connection refused"), opOpen)
	if errs.CodeOf(down) != service.CodeBlobUnavailable {
		t.Errorf("a store that is down was reported as %q", errs.CodeOf(down))
	}
}

// The constructor's own refusal travels through the same helper, and
// re-wrapping it would bury the field it names.
func TestClassifyLeavesAnAlreadyClassifiedErrorAlone(t *testing.T) {
	t.Parallel()

	original := errs.New(errs.KindInvalidArgument, "already classified")

	if classified := classify(original, opPut); !errors.Is(classified, errs.KindInvalidArgument) {
		t.Errorf("classify re-wrapped a classified error as %v", classified)
	}
}

func TestClassifyPassesNilThrough(t *testing.T) {
	t.Parallel()

	if err := classify(nil, opPut); err != nil {
		t.Errorf("classify(nil) = %v", err)
	}
}
