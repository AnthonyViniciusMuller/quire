package discover_test

import (
	"errors"
	"testing"

	"github.com/anthonyvsmuller/quire/internal/federation/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/federation/application/service"
	"github.com/anthonyvsmuller/quire/internal/federation/application/usecase/discover"
	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

func TestExecute(t *testing.T) {
	t.Parallel()

	discovery := apptest.NewDiscovery()
	discovery.Publish(apptest.Descriptor("quire-b.example"))

	output, err := discover.New(discovery).Execute(t.Context(),
		discover.Input{Domain: "quire-b.example"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	switch {
	case output.Descriptor.Domain != "quire-b.example":
		t.Errorf("Domain = %q", output.Descriptor.Domain)
	case output.Descriptor.GRPCAuthority.IsZero():
		t.Error("the address replication would dial was not carried out of the lookup")
	case output.Descriptor.CertificateFingerprint.IsZero():
		t.Error("the pin was not carried out of the lookup")
	}
}

// TestExecuteFoldsTheDomain covers what a reader types. Quire-A.Example is the
// node quire-a.example is, and two spellings that reached two rows would be
// two pins for one key.
func TestExecuteFoldsTheDomain(t *testing.T) {
	t.Parallel()

	discovery := apptest.NewDiscovery()
	discovery.Publish(apptest.Descriptor("quire-b.example"))

	output, err := discover.New(discovery).Execute(t.Context(),
		discover.Input{Domain: "  Quire-B.Example  "})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if output.Descriptor.Domain != "quire-b.example" {
		t.Errorf("Domain = %q, want the folded form", output.Descriptor.Domain)
	}
}

// TestExecuteRefusesSomethingThatIsNotAHostBeforeAsking is why the domain is
// validated here as well as in the adapter: the value goes into the authority
// of a URL, and one that needs escaping there could address the lookup
// somewhere nobody named.
func TestExecuteRefusesSomethingThatIsNotAHostBeforeAsking(t *testing.T) {
	t.Parallel()

	discovery := apptest.NewDiscovery()

	_, err := discover.New(discovery).Execute(t.Context(),
		discover.Input{Domain: "https://quire-b.example/nodes"})
	if err == nil {
		t.Fatal("Execute with something that is not a host = nil, want an error")
	}

	if !errors.Is(err, errs.KindInvalidArgument) || errs.CodeOf(err) != server.CodeInvalidDomain {
		t.Errorf("error = %v, want an invalid argument coded %q", err, server.CodeInvalidDomain)
	}

	if lookups := discovery.Lookups(); len(lookups) != 0 {
		t.Errorf("the lookup was made anyway, against %v", lookups)
	}
}

// TestExecuteStoresNothing is the whole of this use case's contract. A lookup
// that wrote a row would make reading about a node indistinguishable from
// adopting it, and a reader who was only looking would have adopted its pin.
func TestExecuteStoresNothing(t *testing.T) {
	t.Parallel()

	discovery := apptest.NewDiscovery()
	discovery.Publish(apptest.Descriptor("quire-b.example"))

	// The use case is built with nothing it could write through, which is the
	// strongest form this assertion takes: it holds one dependency, and that
	// dependency reads.
	if _, err := discover.New(discovery).Execute(t.Context(),
		discover.Input{Domain: "quire-b.example"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if lookups := discovery.Lookups(); len(lookups) != 1 {
		t.Errorf("lookups = %v, want exactly the one the reader asked for", lookups)
	}
}

// TestExecutePassesOnWhatTheLookupSaid covers the failures the reader has to
// be able to tell apart: a host that is not a node is not worth retrying, and
// a peer that is down is.
func TestExecutePassesOnWhatTheLookupSaid(t *testing.T) {
	t.Parallel()

	discovery := apptest.NewDiscovery()

	_, err := discover.New(discovery).Execute(t.Context(), discover.Input{Domain: "quire-b.example"})
	if err == nil {
		t.Fatal("Execute against a host that publishes nothing = nil, want an error")
	}

	if got := errs.CodeOf(err); got != service.CodeNotAQuireServer {
		t.Errorf("code = %q, want %q", got, service.CodeNotAQuireServer)
	}

	discovery.Err = errs.New(errs.KindUnavailable, "that domain did not answer the lookup").
		WithCode(service.CodeDiscoveryUnreachable)

	_, err = discover.New(discovery).Execute(t.Context(), discover.Input{Domain: "quire-b.example"})
	if !errs.Retryable(err) {
		t.Errorf("error = %v, and a peer that was simply down is worth retrying", err)
	}
}
