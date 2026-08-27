-- The groupings a reader defined over their collection (RF05, UC03).
--
-- A collection and a category are the same row with a different kind, which is
-- what lets RF05 offer both without a second table. Nothing here branches on
-- the value.
--
-- Deleting a grouping does not delete what was on it. The works survive their
-- shelf; what is tombstoned with the grouping are the memberships, by the
-- statements in memberships.sql and in the same transaction.

-- name: CreateCollection :exec
INSERT INTO library.collections (id, user_id, name, kind, description, created_at, vector_clock,
                                 updated_at, device_id, deleted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: UpdateCollection :execrows
UPDATE library.collections
SET name         = $2,
    kind         = $3,
    description  = $4,
    vector_clock = $5,
    updated_at   = $6,
    device_id    = $7,
    deleted      = $8
WHERE id = $1;

-- name: GetCollectionByID :one
SELECT id, user_id, name, kind, description, created_at, vector_clock, updated_at, device_id, deleted
FROM library.collections
WHERE id = $1;

-- The same read, holding the row until the transaction ends.
--
-- It is what makes deleting a grouping and filing a work under it at the same
-- moment reach one answer. Both read the grouping and then write a membership
-- that references it, and under READ COMMITTED a read cannot see a write
-- committed after its own statement began — so without the lock the filing
-- would be written against a grouping that had been tombstoned in between, and
-- the work would sit on a shelf no reply mentions.
--
-- Outside a transaction it locks nothing worth having: the lock is released
-- with the statement. The port says so.
-- name: GetCollectionByIDForUpdate :one
SELECT id, user_id, name, kind, description, created_at, vector_clock, updated_at, device_id, deleted
FROM library.collections
WHERE id = $1
    FOR UPDATE;

-- A reader's groupings, ordered by name so that the list does not reshuffle
-- between two calls, with the identifier as the tie-break because names are
-- not unique — a reader may well have two shelves called "later".
--
-- The optional filter is the reverse of the one a page of works offers: it
-- narrows the reply to the groupings one work is filed under.
-- name: ListCollections :many
SELECT id, user_id, name, kind, description, created_at, vector_clock, updated_at, device_id, deleted
FROM library.collections
WHERE user_id = sqlc.arg(user_id)
  AND NOT deleted
  AND (NOT sqlc.arg(holding_ebook)::boolean
    OR EXISTS (SELECT 1
               FROM library.ebook_collections
               WHERE library.ebook_collections.collection_id = library.collections.id
                 AND library.ebook_collections.ebook_id = sqlc.arg(ebook_id)
                 AND NOT library.ebook_collections.deleted))
ORDER BY name, id;
