package server_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/domain/server"
	"github.com/anthonyvsmuller/quire/internal/shared/errs"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

// discovered is the instant the descriptions below were learned at.
var discovered = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// peer is a node as its discovery document describes it.
func peer() *server.Descriptor {
	return &server.Descriptor{
		Domain:                 "quire-b.example",
		BaseURL:                "https://quire-b.example",
		JWKSURI:                "https://quire-b.example/.well-known/jwks.json",
		CertificateFingerprint: server.Fingerprint(wellknown.PinPrefix + "Zm9vYmFyCg=="),
		GRPCAuthority:          "quire-b.example:9090",
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	node, err := server.New(peer(), discovered)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	switch {
	case node.ID == (uuid.UUID{}):
		t.Error("the node was recorded without an identifier")
	case node.IsLocal:
		t.Error("a discovered peer claims to be this instance, which the unique index allows one row of")
	case !node.Active:
		t.Error("a node the reader just added does not take part in replication")
	case !node.Pinned():
		t.Error("the pin RNF08 is checked against was not carried across")
	case node.GRPCAuthority != "quire-b.example:9090":
		t.Error("the address replication dials was not carried across (D06)")
	case !node.DiscoveredAt.Equal(discovered):
		t.Error("the node does not say when its description was learned")
	}
}

// TestNewWithoutADiscoveryTime covers what the column means: every field of the
// description was learned at some instant, and a row that cannot say when was
// not discovered.
func TestNewWithoutADiscoveryTime(t *testing.T) {
	t.Parallel()

	_, err := server.New(peer(), time.Time{})
	if err == nil {
		t.Fatal("New without a discovery time = nil, want an error")
	}

	assertInvalidArgument(t, err, server.CodeInvalidServer, "discovered_at")
}

func TestNewRejectsADescriptionItCannotStore(t *testing.T) {
	t.Parallel()

	descriptor := peer()
	descriptor.Domain = "HTTPS://Quire-B.Example"

	if _, err := server.New(descriptor, discovered); err == nil {
		t.Fatal("New with a domain that is not a host = nil, want an error")
	}
}

// TestRefreshReportsAMovedPin is the reporting half of C12. The new pin is
// applied — there is nothing here to check it against, and withholding it
// would leave a record that cannot be used — and the reader is told, because a
// rotation and an interception look identical from this side.
func TestRefreshReportsAMovedPin(t *testing.T) {
	t.Parallel()

	node, err := server.New(peer(), discovered)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rotated := peer()
	rotated.CertificateFingerprint = server.Fingerprint(wellknown.PinPrefix + "YmF6cXV1eAo=")

	later := discovered.Add(24 * time.Hour)

	changed, err := node.Refresh(rotated, later)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	switch {
	case !changed:
		t.Error("a node presenting a different key was not reported as one, so the reader cannot look at it")
	case node.CertificateFingerprint != rotated.CertificateFingerprint:
		t.Error("the pin on record is still the old one, which nothing would match")
	case !node.DiscoveredAt.Equal(later):
		t.Error("the node does not say when the description was last learned")
	}
}

// TestRefreshOfAnUnchangedNodeReportsNothing covers the ordinary case, which
// has to be quiet: an ACME renewal keeps the key, so the pin survives it, and
// a refresh that cried rotation every sixty days is the alarm C12 exists to
// stop an operator from learning to ignore.
func TestRefreshOfAnUnchangedNodeReportsNothing(t *testing.T) {
	t.Parallel()

	node, err := server.New(peer(), discovered)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	renewed := peer()
	renewed.BaseURL = "https://quire-b.example:8443"

	changed, err := node.Refresh(renewed, discovered.Add(time.Hour))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if changed {
		t.Error("a node that kept its key was reported as having rotated it")
	}

	if node.BaseURL != renewed.BaseURL {
		t.Error("what the refresh learned was not written back")
	}
}

// TestRefreshRefusesAnotherNode covers the identity of a row: the lookup is
// addressed to the domain on record, so an answer under a different one is not
// a refresh of this node. Accepting it would move a node's identity without
// the reader ever naming the new one.
func TestRefreshRefusesAnotherNode(t *testing.T) {
	t.Parallel()

	node, err := server.New(peer(), discovered)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	elsewhere := peer()
	elsewhere.Domain = "quire-c.example"

	if _, err := node.Refresh(elsewhere, discovered); err == nil {
		t.Fatal("Refresh with another node's description = nil, want an error")
	} else if got := errs.CodeOf(err); got != server.CodeDomainMismatch {
		t.Errorf("code = %q, want %q", got, server.CodeDomainMismatch)
	}

	if node.Domain != "quire-b.example" {
		t.Error("the row now names a node the reader never added")
	}
}

// TestThisInstanceIsNeitherEditableNorRemovable covers the one row every
// reader hosted here references. Deactivating it would stop the node
// replicating on its own behalf; removing it would leave a node that does not
// know who it is, if the foreign keys let it happen at all.
func TestThisInstanceIsNeitherEditableNorRemovable(t *testing.T) {
	t.Parallel()

	local := server.Restore(uuid.New(), &server.Props{
		Descriptor:   server.Descriptor{Domain: "quire-a.example", BaseURL: "https://quire-a.example"},
		IsLocal:      true,
		DiscoveredAt: discovered,
		Active:       true,
	})

	err := local.SetActive(false)
	if err == nil {
		t.Fatal("SetActive on this instance = nil, want an error")
	}

	if !errors.Is(err, errs.KindFailedPrecondition) || errs.CodeOf(err) != server.CodeLocalServer {
		t.Errorf("error = %v, want a failed precondition coded %q", err, server.CodeLocalServer)
	}

	if !local.Active {
		t.Error("the refusal still deactivated this node")
	}

	if err := local.Removable(); err == nil {
		t.Error("Removable on this instance = nil, and forgetting it would orphan every reader hosted here")
	}
}

// TestAPeerIsEditableAndRemovable is the other half: everything the refusal
// above protects is specific to this instance.
func TestAPeerIsEditableAndRemovable(t *testing.T) {
	t.Parallel()

	node, err := server.New(peer(), discovered)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := node.SetActive(false); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	if node.Active {
		t.Error("the node still takes part in replication")
	}

	if err := node.Removable(); err != nil {
		t.Errorf("Removable = %v, want nil", err)
	}
}
