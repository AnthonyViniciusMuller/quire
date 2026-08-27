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
-- If this node is renamed, the insert collides with the partial unique index
-- rather than leaving two rows claiming to be this instance — which is the
-- right outcome: a node whose domain changed has a catalogue that needs an
-- operator, not a second identity.
-- name: EnsureLocalServer :one
INSERT INTO federation.servers (domain, base_url, jwks_uri, is_local, discovered_at, active)
VALUES ($1, $2, $3, true, now(), true)
ON CONFLICT (domain) DO UPDATE
    SET base_url = EXCLUDED.base_url,
        jwks_uri = EXCLUDED.jwks_uri,
        is_local = true,
        active   = true
RETURNING id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active;

-- A peer, with the identifier the entity minted. is_local is false here and in
-- no other statement: EnsureLocalServer above is the only writer of the flag.
-- name: CreateServer :exec
INSERT INTO federation.servers (id, domain, base_url, jwks_uri, certificate_fingerprint,
                                is_local, discovered_at, active)
VALUES ($1, $2, $3, $4, $5, false, $6, $7);

-- What a refresh learned, and whether the node takes part.
-- name: UpdateServer :execrows
UPDATE federation.servers
SET base_url                = $2,
    jwks_uri                = $3,
    certificate_fingerprint = $4,
    discovered_at           = $5,
    active                  = $6
WHERE id = $1;

-- name: DeleteServer :execrows
DELETE FROM federation.servers
WHERE id = $1;

-- name: GetServerByID :one
SELECT id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active
FROM federation.servers
WHERE id = $1;

-- The lookup UC12 addresses by name, and the one that tells an addition from a
-- node the catalogue already holds.
-- name: GetServerByDomain :one
SELECT id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active
FROM federation.servers
WHERE domain = $1;

-- Ordered by domain, which is unique, so the list does not reshuffle between
-- two calls and needs no tie-break. Deactivated nodes are hidden unless asked
-- for: they are still known, and what they are not is replicated to.
-- name: ListServers :many
SELECT id, domain, base_url, jwks_uri, certificate_fingerprint, is_local, discovered_at, active
FROM federation.servers
WHERE active
   OR sqlc.arg(include_inactive)::boolean
ORDER BY domain;
