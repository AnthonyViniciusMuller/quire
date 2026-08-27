// Package replica is a reader's permission for one node to hold a copy of
// their data: the entity, and the port a repository has to satisfy.
//
// This is the whole of the sovereignty claim in RN03. Nothing leaves this node
// for a peer that does not have an active row here, so the table is not a
// cache of a decision made elsewhere — it is the decision (RF16, UC15).
//
// Revoking clears a flag rather than deleting the row, and the reason is worth
// stating plainly: revocation stops the replication, it does not reach into
// another operator's database. The record that the permission once existed is
// what explains a peer that still holds data, and a reader who cannot see that
// record cannot act on it.
package replica

import (
	"time"
	"uuid"

	"github.com/anthonyvsmuller/quire/internal/shared/errs"
)

// opNew is the operation reported by the constructor.
const opNew = "federation/replica: new"

// CodeInvalidAuthorization is an authorization that could not be granted: no
// reader, no node, or no instant.
const CodeInvalidAuthorization = "invalid_replica_authorization"

// Props is everything about an authorization other than its identifier.
type Props struct {
	// UserID is the reader whose data may be copied. The permission is theirs
	// and nobody else's, which is why the catalogue being node-wide does not
	// make the copying node-wide too.
	UserID uuid.UUID

	// ServerID is the node the copy may live on.
	ServerID uuid.UUID

	// AuthorizedAt is when the permission was last granted. A revocation
	// leaves it alone: it says when the reader decided, and a re-grant is a
	// new decision.
	AuthorizedAt time.Time

	// ReplicatesFiles is whether the permission covers the e-book files as
	// well as the metadata. Metadata without the files is the cheap replica a
	// reader is most likely to want on a node they do not own, and it is why
	// library.ebooks does not reference the stored objects (D02).
	ReplicatesFiles bool

	// Active is whether the permission stands.
	Active bool
}

// Replica is one reader's permission for one node (MER: replica_usuario;
// federation.user_replicas).
type Replica struct {
	// ID is the primary key. One row per (reader, node) pair, reused as the
	// decision changes, so that a grant and its revocation stay in one place.
	ID uuid.UUID

	Props
}

// New grants the permission.
func New(userID, serverID uuid.UUID, replicatesFiles bool, at time.Time) (*Replica, error) {
	invalid := func(field, reason string) error {
		return errs.New(errs.KindInvalidArgument, "the authorization could not be granted").
			WithOp(opNew).
			WithCode(CodeInvalidAuthorization).
			WithField(field, reason)
	}

	switch {
	case userID == (uuid.UUID{}):
		return nil, invalid("user_id", "an authorization must name the reader whose data it covers")
	case serverID == (uuid.UUID{}):
		return nil, invalid("server_id", "an authorization must name the node that may hold the copy")
	case at.IsZero():
		return nil, invalid("authorized_at", "an authorization must say when the reader granted it")
	}

	return &Replica{
		ID: uuid.New(),
		Props: Props{
			UserID:          userID,
			ServerID:        serverID,
			AuthorizedAt:    at,
			ReplicatesFiles: replicatesFiles,
			Active:          true,
		},
	}, nil
}

// Restore rebuilds an authorization already stored.
func Restore(id uuid.UUID, props *Props) *Replica {
	return &Replica{ID: id, Props: *props}
}

// Grant re-authorizes a node the reader had revoked, or changes whether the
// files travel with the metadata. It is the same row either way, because the
// pair is what the permission is about.
func (r *Replica) Grant(replicatesFiles bool, at time.Time) {
	r.ReplicatesFiles = replicatesFiles
	r.AuthorizedAt = at
	r.Active = true
}

// Revoke withdraws the permission. The row stays, for the reason the package
// comment gives.
func (r *Replica) Revoke() { r.Active = false }

// BelongsTo reports whether the authorization is the reader's.
//
// Every call that names one has to make this check. An authorization that
// belongs to somebody else is answered exactly as one that does not exist, or
// the reply would tell a reader which identifiers are somebody else's.
func (r *Replica) BelongsTo(userID uuid.UUID) bool { return r.UserID == userID }
