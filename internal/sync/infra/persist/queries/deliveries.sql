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

-- What is still owed to one node, in log order.
--
-- The backoff is in the predicate rather than in the worker, because the
-- alternative is reading rows in order to skip them: a peer that has been
-- unreachable for a week has every one of its rows waiting, and a worker that
-- filtered in Go would page through all of them on every tick. Doubling once
-- per attempt is bounded by the caller, which passes the exponent ceiling.
--
-- The order is the log's and nothing but the log's, and the join is what buys
-- it: a peer is offered a reader's changes in the order this node committed
-- them, so a batch cannot carry an update ahead of the insert it depends on —
-- which the reconciler at the far end would refuse. This query once put the
-- rows never tried ahead of the rest, and that is exactly how an update
-- overtakes its insert: the insert fails with the batch it was in and backs
-- off, the update is written afterwards and has never been tried, and the
-- next pass offers the update alone. The row identifier would have been the
-- other cheap tie-break and is just as wrong: it is a random uuid, so it would
-- have shuffled a reader's history into an order no node could apply.
--
-- deliveries_pending_idx serves the destination and the backoff, and its
-- partial predicate keeps it the size of the backlog rather than of the
-- history.
-- name: ListPendingDeliveries :many
SELECT d.id, d.operation_id, d.server_id, d.applied_at, d.attempts, d.last_attempt_at, d.last_error
FROM sync.deliveries d
    JOIN sync.operations o ON o.id = d.operation_id
WHERE d.server_id = sqlc.arg(server_id)
  AND d.applied_at IS NULL
  AND (d.last_attempt_at IS NULL
       OR d.last_attempt_at < sqlc.arg(now)::timestamptz
            - sqlc.arg(backoff_seconds)::double precision
              * power(2, least(d.attempts, sqlc.arg(max_exponent)::integer))
              * interval '1 second')
ORDER BY o.user_id, o.position
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

-- Owe every peer everything the reader has authorized it for and it has not
-- been offered yet.
--
-- This is where the queue is filled, and it is filled here rather than by the
-- call that stored the operation, for a reason that only shows up on the second
-- peer. A node authorized as a replica today holds none of the reader's history
-- (RF16, UC15), and rows written when the change happened would carry only what
-- happened afterwards — the peer would be permanently missing everything from
-- before its own authorization, and nothing would ever notice. Filling the
-- queue from the log instead makes the two cases one: a peer authorized a
-- moment ago and a peer that missed a week are both simply owed what they have
-- not been offered.
--
-- The subquery is the watermark that keeps it from being a scan of the whole
-- log on every tick. Deliveries are enqueued in position order and never
-- skipped, so the highest position already owed to a pair is a floor for what
-- is left, and the ON CONFLICT is what makes the statement safe when it is not.
--
-- This node's own row is excluded, and so is a peer discovery has learned about
-- but the operator has stopped: an inactive node keeps what it already owes and
-- is offered nothing new.
-- name: EnqueuePendingDeliveries :execrows
INSERT INTO sync.deliveries (operation_id, server_id)
SELECT o.id, r.server_id
FROM federation.user_replicas r
    JOIN federation.servers s ON s.id = r.server_id
    JOIN sync.operations o ON o.user_id = r.user_id
WHERE r.active
  AND s.active
  AND NOT s.is_local
  AND o.position > COALESCE((
      SELECT max(owed.position)
      FROM sync.deliveries d
          JOIN sync.operations owed ON owed.id = d.operation_id
      WHERE d.server_id = r.server_id
        AND owed.user_id = r.user_id
  ), 0)
ON CONFLICT (operation_id, server_id) DO NOTHING;
