package gcs

import (
	"errors"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/anthonyvsmuller/quire/internal/library/application/service"
	"github.com/anthonyvsmuller/quire/internal/shared/config"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// A service account key the node cannot read is a deployment fault, and the
// node has to report it while it is starting rather than at the first import.
func TestNewRefusesACredentialsFileItCannotRead(t *testing.T) {
	t.Parallel()

	_, err := New(t.Context(), &config.Storage{
		Bucket: "quire-contents",
		GCS:    config.StorageGCS{CredentialsFile: "/nonexistent/service-account.json"},
	})

	if !errors.Is(err, errs.KindFailedPrecondition) {
		t.Errorf("New = %v, want a failed precondition", err)
	}

	if fields := errs.FieldsOf(err); len(fields) != 1 || fields[0].Name != "QUIRE_STORAGE_GCS_CREDENTIALS_FILE" {
		t.Errorf("the refusal points at %v, want the variable at fault", fields)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		kind errs.Kind
	}{
		"an object that is not there": {storage.ErrObjectNotExist, errs.KindNotFound},
		"the wrong bucket":            {storage.ErrBucketNotExist, errs.KindNotFound},
		"a store that is down":        {errors.New("dial tcp: connection refused"), errs.KindUnavailable},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := classify(testCase.err, opOpen); !errors.Is(err, testCase.kind) {
				t.Errorf("classify(%v) is not %v: %v", testCase.err, testCase.kind, err)
			}
		})
	}
}

func TestClassifyNamesTheCodeTheCallerActsOn(t *testing.T) {
	t.Parallel()

	missing := classify(storage.ErrObjectNotExist, opOpen)
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
