-- Which works are filed under which grouping (MER: ebook_colecao).
--
-- The row is a register that is set and cleared, never one that is appended
-- and removed. C06 in docs/tcc-corrections.md is why: Quadro 20 has no
-- uniqueness constraint, so nothing in the specification stops the same work
-- from being filed twice in the same grouping, which is exactly what two
-- offline devices will do. The pair is the natural key, and filing a work that
-- is already filed reuses the row and flips its tombstone.
--
-- Nothing here computes a revision. Clearing every filing of a work is a loop
-- in the repository over the rows below rather than one UPDATE with a
-- jsonb_set expression, because the stamping rule is C01's and it already
-- exists once, in crdt.Revision — a SET clause that ticked a vector clock and
-- stepped a timestamp would be a second copy of a rule the whole convergence
-- argument rests on, in a language where it could not be tested against the
-- first.

-- name: CreateMembership :exec
INSERT INTO library.ebook_collections (id, ebook_id, collection_id, vector_clock, updated_at,
                                       device_id, deleted)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: UpdateMembership :execrows
UPDATE library.ebook_collections
SET vector_clock = $2,
    updated_at   = $3,
    device_id    = $4,
    deleted      = $5
WHERE id = $1;

-- The register of one pair, set or cleared. A pair that has never been written
-- is no row, which the caller answers by creating one rather than by failing.
-- name: GetMembershipByPair :one
SELECT id, ebook_id, collection_id, vector_clock, updated_at, device_id, deleted
FROM library.ebook_collections
WHERE ebook_id = $1
  AND collection_id = $2;

-- Every filing of one work that is still set, which is what deleting the work
-- has to clear. Rows already cleared are skipped: stamping a new revision on
-- them would claim a write that did not happen.
-- name: ListFiledMembershipsForEbook :many
SELECT id, ebook_id, collection_id, vector_clock, updated_at, device_id, deleted
FROM library.ebook_collections
WHERE ebook_id = $1
  AND NOT deleted
ORDER BY id;

-- Every filing under one grouping that is still set, which is what deleting
-- the grouping has to clear. The works themselves survive it.
-- name: ListFiledMembershipsForCollection :many
SELECT id, ebook_id, collection_id, vector_clock, updated_at, device_id, deleted
FROM library.ebook_collections
WHERE collection_id = $1
  AND NOT deleted
ORDER BY id;
