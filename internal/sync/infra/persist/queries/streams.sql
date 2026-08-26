-- Allocate this node's next position in a user's operation log.
--
-- The statement is the whole of C08 in docs/tcc-corrections.md. The upsert
-- takes the row lock and holds it until the transaction commits, so a second
-- writer cannot obtain its number before the first has committed: the order of
-- the numbers is the order of the commits, and a reader that has seen position
-- N has necessarily seen every position below it. That is what a cursor needs
-- and what neither a timestamp nor a sequence provides, since both are assigned
-- when the row is written rather than when it becomes visible.
--
-- The ON CONFLICT branch also means a user's stream needs no separate creation:
-- the first operation opens it.
--
-- It has to run inside the same transaction as the INSERT into sync.operations.
-- Allocated in one transaction and used in another, the lock is released before
-- the operation is visible and the guarantee is gone.

-- name: AllocatePosition :one
INSERT INTO sync.streams (user_id, last_position)
VALUES ($1, 1)
ON CONFLICT (user_id) DO UPDATE
    SET last_position = sync.streams.last_position + 1
RETURNING last_position;
