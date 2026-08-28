-- What this node still owes its peers, one row per operation and destination
-- (MER: entrega_sync, the entity C07 in docs/tcc-corrections.md splits out of
-- operacao_sync).
--
-- The table is a queue and not a history. A row exists because a change has to
-- reach a node and has not yet; what closes it is the destination's own
-- confirmation, which is safe to act on precisely because receiving is
-- idempotent by operation identifier — a delivery retried after a reply that
-- was lost costs one call and changes nothing there.

-- Owe an operation to each of the nodes.
--
-- The pair is unique, so a second enqueue of the same pair does nothing: the
-- statement is written to be safe under the retry the transaction manager
-- performs after a serialization failure, and under an operation that arrives
-- again from a second peer.
--
-- It must run in the transaction that appended the operation. A change
-- committed without its delivery rows is a change no worker will ever send,
-- and nothing afterwards would notice.
-- name: EnqueueDeliveries :exec
INSERT INTO sync.deliveries (operation_id, server_id)
SELECT sqlc.arg(operation_id), unnest(sqlc.arg(server_ids)::uuid[])
ON CONFLICT (operation_id, server_id) DO NOTHING;

-- What is still owed to one node, oldest attempt first.
--
-- The backoff is in the predicate rather than in the worker, because the
-- alternative is reading rows in order to skip them: a peer that has been
-- unreachable for a week has every one of its rows waiting, and a worker that
-- filtered in Go would page through all of them on every tick. Doubling once
-- per attempt is bounded by the caller, which passes the exponent ceiling.
--
-- deliveries_pending_idx serves the ordering and the destination, and its
-- partial predicate keeps it the size of the backlog rather than of the
-- history.
-- name: ListPendingDeliveries :many
SELECT id, operation_id, server_id, applied_at, attempts, last_attempt_at, last_error
FROM sync.deliveries
WHERE server_id = sqlc.arg(server_id)
  AND applied_at IS NULL
  AND (last_attempt_at IS NULL
       OR last_attempt_at < sqlc.arg(now)::timestamptz
            - sqlc.arg(backoff_seconds)::double precision
              * power(2, least(attempts, sqlc.arg(max_exponent)::integer))
              * interval '1 second')
ORDER BY last_attempt_at NULLS FIRST, id
LIMIT sqlc.arg(page_size)::integer;

-- The nodes this instance owes anything to at all.
--
-- The worker asks this rather than walking the catalogue, because the two are
-- different sets: a node authorized a moment ago is owed nothing yet, and a
-- node whose authorization was revoked may still be owed what was enqueued
-- before the revocation.
-- name: ListPendingServers :many
SELECT DISTINCT server_id
FROM sync.deliveries
WHERE applied_at IS NULL;

-- Close the deliveries of one batch that the destination confirmed.
--
-- Already-confirmed rows are excluded rather than rewritten, so a reply that
-- arrives twice does not count a second attempt or move the instant the
-- operation was applied at.
-- name: ConfirmDeliveries :execrows
UPDATE sync.deliveries
SET applied_at      = sqlc.arg(attempted_at),
    attempts        = attempts + 1,
    last_attempt_at = sqlc.arg(attempted_at),
    last_error      = NULL
WHERE server_id = sqlc.arg(server_id)
  AND operation_id = ANY (sqlc.arg(operation_ids)::uuid[])
  AND applied_at IS NULL;

-- Count a try that did not land, and record what it said.
--
-- The count is what the backoff above is computed from, which is why a worker
-- must record a failure rather than merely logging it: a failure nobody
-- counted is a peer retried at full rate for ever.
-- name: FailDeliveries :execrows
UPDATE sync.deliveries
SET attempts        = attempts + 1,
    last_attempt_at = sqlc.arg(attempted_at),
    last_error      = sqlc.arg(reason)
WHERE server_id = sqlc.arg(server_id)
  AND operation_id = ANY (sqlc.arg(operation_ids)::uuid[])
  AND applied_at IS NULL;
