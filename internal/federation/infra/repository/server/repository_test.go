package server

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/infra/persist/federationdb"
	"github.com/anthonyvsmuller/quire/internal/shared/wellknown"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	jwks := "https://quire-b.example/.well-known/jwks.json"
	pin := wellknown.PinPrefix + "Zm9vYmFyCg=="

	node := toDomain(&federationdb.FederationServer{
		ID:                     id,
		Domain:                 "quire-b.example",
		BaseUrl:                "https://quire-b.example",
		JwksUri:                &jwks,
		CertificateFingerprint: &pin,
		DiscoveredAt:           &at,
		Active:                 true,
	})

	switch {
	case node.ID != id:
		t.Error("the row was rebuilt under a new identifier, so the readers hosted here would be orphaned")
	case node.Domain != "quire-b.example" || node.BaseURL != "https://quire-b.example":
		t.Errorf("the description was not carried across: %+v", node.Props)
	case node.JWKSURI.String() != jwks:
		t.Error("the signing key location was lost, so this node could not verify the peer's tokens")
	case !node.Pinned() || node.CertificateFingerprint.String() != pin:
		t.Error("the pin RNF08 is checked against was lost")
	case !node.DiscoveredAt.Equal(at):
		t.Error("the row no longer says when its description was learned")
	case node.IsLocal:
		t.Error("a peer came back claiming to be this instance")
	}
}

// TestToDomainOfANodeWithoutAPin covers the development profile, and any peer
// whose document publishes neither a key location nor a certificate: the three
// nullable columns are absent, and absence has to survive the round trip as
// absence rather than as an empty value the caller would try to use.
func TestToDomainOfANodeWithoutAPin(t *testing.T) {
	t.Parallel()

	node := toDomain(&federationdb.FederationServer{
		ID:      uuid.New(),
		Domain:  "quire-dev.example",
		BaseUrl: "http://127.0.0.1:8080",
		IsLocal: true,
		Active:  true,
	})

	switch {
	case node.Pinned():
		t.Error("a node that published no certificate reported a pin")
	case !node.JWKSURI.IsZero():
		t.Error("a node that published no keys reported a location for them")
	case !node.DiscoveredAt.IsZero():
		t.Error("a row nothing discovered reported a discovery time")
	case !node.IsLocal:
		t.Error("the row that is this instance came back as a peer")
	}

	// The local row is the one every reader hosted here references, and the
	// domain refuses to edit or forget it whichever repository read it.
	if err := node.Removable(); err == nil {
		t.Error("the restored local row is removable")
	}
}

// TestToDomainRestores covers what Restore is for: a description read back has
// to satisfy the same rules a discovered one did, so that an entity that
// exists is an entity that is valid.
func TestToDomainRestores(t *testing.T) {
	t.Parallel()

	node := toDomain(&federationdb.FederationServer{
		ID:      uuid.New(),
		Domain:  "quire-b.example",
		BaseUrl: "https://quire-b.example",
		Active:  true,
	})

	if err := node.Validate(); err != nil {
		t.Errorf("a row read back does not validate: %v", err)
	}
}

func TestOptionalString(t *testing.T) {
	t.Parallel()

	if optionalString("") != nil {
		t.Error("an absent value was written as an empty string rather than as NULL")
	}

	if got := optionalString("value"); got == nil || *got != "value" {
		t.Errorf("optionalString = %v, want a pointer to the value", got)
	}
}

func TestOptionalTime(t *testing.T) {
	t.Parallel()

	if optionalTime(time.Time{}) != nil {
		t.Error("the zero instant was written as a time rather than as NULL")
	}
}
