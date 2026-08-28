-- The log of changes this node holds for its readers (RF10, RF12; UC09, UC11).
--
-- Three properties hold across every statement here and are what make the rest
-- of them small.
--
-- The identifier is the author's. A device mints it and it is the same uuid on
-- every node that ever sees the operation, so receiving is an
-- INSERT ... ON CONFLICT (id) DO NOTHING and no statement here generates one.
--
-- The position is this node's. It is allocated from sync.streams inside the
-- writing transaction, and the whole of C08 in docs/tcc-corrections.md is why
-- it has to be: the row lock is held until commit, so a second writer cannot
-- obtain its number before the first has committed, and the order of the
-- numbers is therefore the order of the commits rather than the order of the
-- inserts.
--
-- The log is append-only. Nothing here updates a row; what changes is the
-- delivery rows that point at it, which are in deliveries.sql.

-- Store an operation, allocating this node's position for it in the same
-- statement.
--
-- The allocation is a data-modifying CTE rather than a separate call, and that
-- is what makes C08's requirement structural instead of a comment somebody has
-- to obey: a statement cannot straddle two transactions, so the lock the upsert
-- takes cannot be released before the operation it numbered is visible.
--
-- A duplicate returns no row, which is how the caller learns that this node
-- already had the operation. It consumes a position doing so, and the gap that
-- leaves is expected and harmless — the cursor is a lower bound on what has
-- been seen, not a count of what exists.
-- name: AppendOperation :one
WITH allocated AS (
    INSERT INTO sync.streams (user_id, last_position)
    VALUES (sqlc.arg(user_id), 1)
    ON CONFLICT (user_id) DO UPDATE
        SET last_position = sync.streams.last_position + 1
    RETURNING last_position
)
INSERT INTO sync.operations (id, user_id, device_id, position, target_entity, target_id,
                             operation, delta, vector_clock, created_at)
SELECT sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(device_id), allocated.last_position,
       sqlc.arg(target_entity), sqlc.arg(target_id), sqlc.arg(operation),
       sqlc.arg(delta), sqlc.arg(vector_clock), sqlc.arg(created_at)
FROM allocated
ON CONFLICT (id) DO NOTHING
RETURNING position;

-- One page of a reader's log, in the order this node committed it (RN06).
--
-- The cursor is a position and not a timestamp, for the reason C08 gives: an
-- operation stamped early and committed late is skipped by a cursor that has
-- already moved past it, and it is not delayed, it is lost.
--
-- operations_user_position_idx serves the whole statement: the reader selects,
-- the ordering is the index order, and the cursor is a seek into it.
-- name: ListOperationsAfter :many
SELECT id, user_id, device_id, position, target_entity, target_id, operation, delta,
       vector_clock, created_at
FROM sync.operations
WHERE user_id = sqlc.arg(user_id)
  AND position > sqlc.arg(after_position)
ORDER BY position
LIMIT sqlc.arg(page_size)::integer;

-- The operations a batch of deliveries owes, by the identifiers their authors
-- minted.
--
-- The order is the reader and then the position, because the caller sends them
-- grouped by reader: the peer-facing call names one reader, since the
-- certificate identifies the node and not any of the readers it replicates.
-- name: ListOperationsByID :many
SELECT id, user_id, device_id, position, target_entity, target_id, operation, delta,
       vector_clock, created_at
FROM sync.operations
WHERE id = ANY (sqlc.arg(ids)::uuid[])
ORDER BY user_id, position;
