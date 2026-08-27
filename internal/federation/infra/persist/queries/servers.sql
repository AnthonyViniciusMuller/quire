-- The catalogue of nodes this instance knows, its own included (RF13, UC12).
--
-- The catalogue is node-wide: federation.servers names no reader, and what is
-- per-reader is the permission in federation.user_replicas. Nothing here is
-- therefore scoped to a caller.
--
-- Two columns are never written by an update. The domain identifies the row
-- across the federation, and is_local is what the partial unique index allows
-- exactly one row to claim; a statement that changed either would either
-- orphan the readers hosted here or make "is this reader local" unanswerable.
--
-- Every column list below ends with grpc_authority rather than reading in the
-- order the record is described in. It is the order the table has, since the
-- column was added by a later migration, and a list in any other order makes
-- sqlc generate a row struct per statement instead of reusing the one model —
-- four near-identical types, and a repository that maps each of them.

-- Create or refresh the row that says which node this is.
--
-- The upsert is on the domain, and it rewrites what a redeployment may have
-- changed — the base URL and the JWKS location — while leaving the identifier
-- alone. That identifier is referenced by every reader hosted here, so a
-- statement that replaced it would orphan them.
--
-- The certificate pin is deliberately absent. A node does not pin itself: it
-- publishes its pin in the discovery document, and what is stored here is what
-- peers were told, not what this node checks.
--
-- The gRPC authority is written, and is the value this node advertises: a
-- catalogue where the local row could not say where the API answers would be
-- one a reader cannot read their own node out of (D06).
--
-- If this node is renamed, the insert collides with the partial unique index
-- rather than leaving two rows claiming to be this instance — which is the
-- right outcome: a node whose domain changed has a catalogue that needs an
-- operator, not a second identity.
-- name: EnsureLocalServer :one
INSERT INTO federation.servers (domain, base_url, jwks_uri, grpc_authority, is_local, discovered_at, active)
VALUES ($1, $2, $3, $4, true, now(), true)
ON CONFLICT (domain) DO UPDATE
    SET base_url       = EXCLUDED.base_url,
        jwks_uri       = EXCLUDED.jwks_uri,
        grpc_authority = EXCLUDED.grpc_authority,
        is_local       = true,
        active         = true
RETURNING id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active,
          grpc_authority;

-- A peer, with the identifier the entity minted. is_local is false here and in
-- no other statement: EnsureLocalServer above is the only writer of the flag.
-- name: CreateServer :exec
INSERT INTO federation.servers (id, domain, base_url, jwks_uri, certificate_fingerprint,
                                grpc_authority, is_local, discovered_at, active)
VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8);

-- What a refresh learned, and whether the node takes part.
-- name: UpdateServer :execrows
UPDATE federation.servers
SET base_url                = $2,
    jwks_uri                = $3,
    certificate_fingerprint = $4,
    grpc_authority          = $5,
    discovered_at           = $6,
    active                  = $7
WHERE id = $1;

-- Forgetting a node, with both refusals in the statement rather than beside
-- it.
--
-- is_local, because every reader hosted here references that row. And no
-- active authorization, because forgetting a node somebody still replicates to
-- would leave that reader unable to revoke a peer holding their data, which is
-- the whole of RN03 — and the foreign key cascades, so a delete that got past
-- the check would take their authorization with it rather than being refused
-- by the database.
--
-- The caller reads the row first, so that it can say which of the two refused
-- it. This statement is what makes the refusal hold anyway: a check the caller
-- ran a moment earlier is a check something could have invalidated since.
-- name: DeleteServerIfUnused :execrows
DELETE FROM federation.servers
WHERE federation.servers.id = $1
  AND NOT federation.servers.is_local
  AND NOT EXISTS (SELECT 1
                  FROM federation.user_replicas
                  WHERE federation.user_replicas.server_id = federation.servers.id
                    AND federation.user_replicas.active);

-- name: GetServerByID :one
SELECT id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active,
       grpc_authority
FROM federation.servers
WHERE id = $1;

-- The lookup UC12 addresses by name, and the one that tells an addition from a
-- node the catalogue already holds.
-- name: GetServerByDomain :one
SELECT id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active,
       grpc_authority
FROM federation.servers
WHERE domain = $1;

-- Ordered by domain, which is unique, so the list does not reshuffle between
-- two calls and needs no tie-break. Deactivated nodes are hidden unless asked
-- for: they are still known, and what they are not is replicated to.
-- name: ListServers :many
SELECT id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active,
       grpc_authority
FROM federation.servers
WHERE active
   OR sqlc.arg(include_inactive)::boolean
ORDER BY domain;
