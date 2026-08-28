-- The readers this node knows (RF06, UC06, UC14).
--
-- Every statement that reads a reader by something other than the primary key
-- takes the origin server as a parameter. RN09 makes the identifier unique on
-- the pair and the address unique only within the origin server, so a lookup
-- that omitted it would answer with a reader of another node — and on a node
-- that replicates for a peer, rows of both kinds sit in this table.

-- name: CreateUser :exec
INSERT INTO identity.users (
    id, origin_server_id, local_name, display_name, email, password_hash, migrated_from,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- Only the four columns UC06 makes writable. The local name and the origin
-- server are the identity itself: changing the first is registering somebody
-- else, and changing the second is the migration of RF17, which rewrites the
-- column deliberately and elsewhere.
--
-- :execrows, so that a caller can tell a reader who was updated from one who no
-- longer exists. An UPDATE matching nothing is not an error to PostgreSQL.
-- name: UpdateUser :execrows
UPDATE identity.users
SET display_name  = $2,
    email         = $3,
    password_hash = $4,
    updated_at    = $5
WHERE id = $1;

-- name: DeleteUser :execrows
DELETE FROM identity.users WHERE id = $1;

-- name: GetUserByID :one
SELECT id, origin_server_id, local_name, display_name, email, password_hash,
       created_at, updated_at, migrated_from
FROM identity.users
WHERE id = $1;

-- name: GetUserByLocalName :one
SELECT id, origin_server_id, local_name, display_name, email, password_hash,
       created_at, updated_at, migrated_from
FROM identity.users
WHERE origin_server_id = $1
  AND local_name = $2;

-- Folded on both sides, because the unique index enforcing RN09 is over
-- lower(email): a lookup that compared the stored capitalization would miss the
-- row that a second registration is about to collide with, and would report the
-- address free right up to the insert that fails.
-- name: GetUserByEmail :one
SELECT id, origin_server_id, local_name, display_name, email, password_hash,
       created_at, updated_at, migrated_from
FROM identity.users
WHERE origin_server_id = $1
  AND lower(email) = lower(sqlc.arg(email)::text);
