-- This node's own row in the catalogue of servers (RF13).
--
-- The catalogue belongs to the federation slice, and the query is here because
-- the identity slice needs it before that slice exists: UC14 binds a reader to
-- the node they registered with, and identity.users.origin_server_id has to
-- point at something. Phase 6 takes the table over; what this file writes is
-- the one row it will already find there.

-- Create or refresh the row that says which node this is.
--
-- The upsert is on the domain, which is what identifies a node across the
-- federation, and it rewrites what a redeployment may have changed — the base
-- URL and the JWKS location — while leaving the identifier alone. That
-- identifier is referenced by every reader hosted here, so a statement that
-- replaced it would orphan them.
--
-- A partial unique index allows one row to claim is_local. If this node is
-- renamed, the insert collides with it rather than leaving two rows claiming to
-- be this instance — which is the right outcome: a node whose domain changed
-- has a catalogue that needs an operator, not a second identity.
-- name: EnsureLocalServer :one
INSERT INTO federation.servers (domain, base_url, jwks_uri, is_local, discovered_at, active)
VALUES ($1, $2, $3, true, now(), true)
ON CONFLICT (domain) DO UPDATE
    SET base_url = EXCLUDED.base_url,
        jwks_uri = EXCLUDED.jwks_uri,
        is_local = true,
        active   = true
RETURNING id;
