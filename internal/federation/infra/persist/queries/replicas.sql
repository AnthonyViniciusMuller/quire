-- Which nodes a reader allows to hold a copy of their data (RF16, UC15).
--
-- Nothing here deletes. Revoking clears active, because the record that the
-- authorization once existed is what explains a peer that still holds data:
-- revocation stops the replication, it does not reach into another operator's
-- database.
--
-- One row per (reader, node) pair, which the unique constraint enforces and
-- which is what makes a re-grant an update rather than a second history.

-- name: CreateReplicaAuthorization :exec
INSERT INTO federation.user_replicas (id, user_id, server_id, authorized_at, replicates_files, active)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateReplicaAuthorization :execrows
UPDATE federation.user_replicas
SET authorized_at    = $2,
    replicates_files = $3,
    active           = $4
WHERE id = $1;

-- Read by the pair rather than by the primary key, because that is what the
-- reader names: a node, and themselves. Revoked rows are returned, since
-- re-authorizing one writes that row rather than a second.
-- name: GetReplicaAuthorizationByPair :one
SELECT id, user_id, server_id, authorized_at, replicates_files, active
FROM federation.user_replicas
WHERE user_id = $1
  AND server_id = $2;

-- Newest decision first, with the identifier breaking the tie between two
-- granted in the same instant.
-- name: ListReplicaAuthorizationsByUser :many
SELECT id, user_id, server_id, authorized_at, replicates_files, active
FROM federation.user_replicas
WHERE user_id = $1
  AND (active OR sqlc.arg(include_inactive)::boolean)
ORDER BY authorized_at DESC, id;

-- How many readers still allow the node to hold a copy, across the whole
-- instance. It is what refuses to forget a node somebody is still replicating
-- to, and it counts every reader because the catalogue is node-wide.
-- name: CountActiveReplicaAuthorizationsForServer :one
SELECT count(*)
FROM federation.user_replicas
WHERE server_id = $1
  AND active;
