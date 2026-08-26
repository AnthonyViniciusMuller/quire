-- Sync: the log of operations exchanged between nodes, and the record of which
-- peer has already received each one.
--
-- MER: operacao_sync (subsection 4.2.4 and Appendix A of the TCC), split in two
-- per C07 in docs/tcc-corrections.md — the operation is one thing and its
-- delivery to a peer is another, and conflating them stores the delta once per
-- destination and gives the same change a different identity on every hop.
--
-- Three properties hold across this schema and are worth stating once:
--
-- An operation's id is the same uuid on every node. The device mints it, and a
-- node that receives the same operation twice — from the device and again from
-- a peer that also replicates the user — recognizes it by that id. Receiving is
-- therefore an INSERT ... ON CONFLICT (id) DO NOTHING, and never generates an
-- id of its own.
--
-- An operation's position is node-local. It is this node's order for this
-- user's log, and two nodes will number the same operations differently. A
-- device pulling from two nodes keeps one cursor per node.
--
-- The log is append-only. An operation is never edited after it is written;
-- what changes is the delivery rows that point at it.

CREATE SCHEMA IF NOT EXISTS sync;

-- The position allocator, one row per user (C08).
--
-- The cursor RN06 delivers deltas from cannot be a timestamp, and cannot be a
-- sequence either: both are assigned when the operation is written, while the
-- row becomes visible when its transaction commits, and those two orders differ
-- whenever one transaction is slower than another. A reader that advances past
-- a position whose transaction had not yet committed never comes back for it.
--
-- Allocating from a row is what closes that gap. The writing transaction runs
--
--   INSERT INTO sync.streams (user_id, last_position) VALUES ($1, 1)
--   ON CONFLICT (user_id) DO UPDATE SET last_position = sync.streams.last_position + 1
--   RETURNING last_position;
--
-- which takes the row lock and holds it until commit. A second transaction
-- cannot obtain its number before the first has committed, so the order of the
-- numbers is the order of the commits: a reader that has seen position N has
-- necessarily seen every position below it. The upsert also means a user's
-- stream needs no separate creation step.
--
-- The cost is that one user's writes serialize against each other. A single
-- reader's write rate makes that free, and their concurrent pushes already
-- contend on the same rows.
CREATE TABLE sync.streams (
    user_id       uuid   PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    last_position bigint NOT NULL DEFAULT 0,

    CONSTRAINT streams_position_not_negative CHECK (last_position >= 0)
);

-- MER: operacao_sync. The log of changes, and the entity the whole replication
-- mechanism turns on (RF10, RF12; UC09, UC11).
CREATE TABLE sync.operations (
    -- Minted by the device that authored the change, and identical on every
    -- node that ever sees it. This is what makes receiving idempotent.
    id            uuid         PRIMARY KEY,

    -- Reachable through device_id, and stored anyway: the position below is
    -- scoped per user, so the uniqueness constraint needs it, and every pull
    -- filters on it. See C08.
    user_id       uuid         NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    device_id     uuid         NOT NULL REFERENCES identity.devices (id) ON DELETE CASCADE,

    -- This node's order for this user's log. Gaps are expected and harmless:
    -- an operation already received consumes a number and then does nothing.
    position      bigint       NOT NULL,

    -- Which record changed. The entity is named logically rather than by table,
    -- because the same name travels in the protobuf contract and in the SQLite
    -- schema on the device.
    target_entity varchar(40)  NOT NULL,
    target_id     uuid         NOT NULL,

    operation     varchar(20)  NOT NULL,

    -- Only the delta, never the whole record (RN06).
    delta         jsonb        NOT NULL,

    -- The causal version the reconciler merges on (RN02).
    vector_clock  jsonb        NOT NULL,

    -- Stamped by the authoring device, so it is subject to C01: it has to be a
    -- hybrid logical clock for the tie-break above it to be a total order.
    created_at    timestamptz  NOT NULL DEFAULT now(),

    CONSTRAINT operations_user_position_key UNIQUE (user_id, position),
    CONSTRAINT operations_position_positive CHECK (position > 0),
    CONSTRAINT operations_kind CHECK (operation IN ('insert', 'update', 'delete')),

    -- The replicable set, spelled out. A typo in an entity name would otherwise
    -- produce operations no reconciler ever applies, and nothing would say so.
    CONSTRAINT operations_target_entity CHECK (target_entity IN (
        'ebook', 'collection', 'ebook_collection', 'reading_progress', 'annotation'
    )),

    CONSTRAINT operations_delta_is_object CHECK (jsonb_typeof(delta) = 'object'),
    CONSTRAINT operations_clock_is_object CHECK (jsonb_typeof(vector_clock) = 'object')
);

-- RN10 says an operation is accepted only when the authenticated device matches
-- the one it declares. The interceptor checks that the caller is the device;
-- this composite reference checks that the device is the user's, so an
-- operation cannot be filed under one reader naming another reader's device.
-- The unique constraint it needs did not belong in identity on its own — it
-- exists for this reference and is created here, next to what uses it.
ALTER TABLE identity.devices
    ADD CONSTRAINT devices_id_user_id_key UNIQUE (id, user_id);

ALTER TABLE sync.operations
    ADD CONSTRAINT operations_device_belongs_to_user
    FOREIGN KEY (device_id, user_id) REFERENCES identity.devices (id, user_id) ON DELETE CASCADE;

-- The pull: everything this user has after the cursor, in order.
CREATE INDEX operations_user_position_idx ON sync.operations (user_id, position);

-- The reconciler: every operation that touched one record, to replay or audit
-- how a value was arrived at.
CREATE INDEX operations_target_idx ON sync.operations (target_entity, target_id);

-- MER: entrega_sync, the entity C07 splits out of operacao_sync. One row per
-- operation and destination node.
--
-- A change destined for three authorized replicas is three rows here and one
-- row above, so the delta is stored once and the operation keeps one identity
-- across the federation.
CREATE TABLE sync.deliveries (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id    uuid        NOT NULL REFERENCES sync.operations (id) ON DELETE CASCADE,
    server_id       uuid        NOT NULL REFERENCES federation.servers (id) ON DELETE CASCADE,

    -- Null until the destination has confirmed it applied the operation, which
    -- is what makes this table the queue the replication worker drains.
    applied_at      timestamptz,

    -- What the worker needs to back off with. A peer belonging to another
    -- operator is unreachable often enough that retrying it at full rate would
    -- be the node's largest source of outbound traffic.
    attempts        integer     NOT NULL DEFAULT 0,
    last_attempt_at timestamptz,
    last_error      text,

    CONSTRAINT deliveries_pair_key UNIQUE (operation_id, server_id),
    CONSTRAINT deliveries_attempts_not_negative CHECK (attempts >= 0)
);

-- What the replication worker asks: what do I still owe this peer, oldest
-- first. The partial predicate keeps the index the size of the backlog rather
-- than the size of the history.
CREATE INDEX deliveries_pending_idx ON sync.deliveries (server_id, last_attempt_at NULLS FIRST)
    WHERE applied_at IS NULL;
