package replica

import (
	"testing"
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/federation/infra/persist/federationdb"
)

func TestToDomain(t *testing.T) {
	t.Parallel()

	id, reader, node := uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	authorization := toDomain(&federationdb.FederationUserReplica{
		ID:              id,
		UserID:          reader,
		ServerID:        node,
		AuthorizedAt:    at,
		ReplicatesFiles: true,
		Active:          true,
	})

	switch {
	case authorization.ID != id:
		t.Error("the row was rebuilt under a new identifier, so the decision would have two histories")
	case !authorization.BelongsTo(reader):
		t.Error("the authorization no longer names the reader whose data it covers")
	case authorization.ServerID != node:
		t.Error("the authorization no longer names the node holding the copy")
	case !authorization.ReplicatesFiles:
		t.Error("a permission covering the files came back covering only the metadata")
	case !authorization.AuthorizedAt.Equal(at):
		t.Error("the row no longer says when the reader decided")
	}
}

// TestToDomainOfARevokedAuthorization covers the row that survives revocation,
// which is what explains a peer that still holds data.
func TestToDomainOfARevokedAuthorization(t *testing.T) {
	t.Parallel()

	authorization := toDomain(&federationdb.FederationUserReplica{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		ServerID:     uuid.New(),
		AuthorizedAt: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC),
		Active:       false,
	})

	if authorization.Active {
		t.Error("a revoked authorization came back standing")
	}
}
