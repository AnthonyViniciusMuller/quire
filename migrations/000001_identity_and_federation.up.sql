-- Identity and federation: the nodes this instance knows, the users hosted on
-- them, the devices that write on a user's behalf, and the credentials that
-- let a device come back.
--
-- MER: servidor, usuario, replica_usuario, dispositivo, token_acesso
-- (subsection 4.2.4 and Appendix A of the TCC). The mapping from the
-- Portuguese entity and attribute names to the ones used here is in
-- docs/mer-mapping.md.
--
-- The two schemas travel in one migration because they reference each other:
-- identity.users names the node that authenticates it, and
-- federation.user_replicas names the user who authorized the replica. Splitting
-- them would produce a state in between that has no valid ordering.

CREATE SCHEMA IF NOT EXISTS federation;
CREATE SCHEMA IF NOT EXISTS identity;

-- MER: servidor. The catalogue of nodes this instance knows, its own included
-- (RF13, UC12).
--
-- Holding the domain and the certificate fingerprint once, here, is what keeps
-- them out of every other table: a user points at the node that hosts them, a
-- replica authorization points at the node that holds the copy, and a sync
-- operation points at the node it is destined for.
CREATE TABLE federation.servers (
    id                      uuid         PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The domain that forms the second half of a federated identifier, the
    -- part after the colon in @anthony:quire-a.example, and the authority the
    -- .well-known lookup is addressed to.
    domain                  varchar(255) NOT NULL,

    -- Where the node actually answers, as returned by discovery (RF14, UC13).
    -- It is separate from the domain on purpose: the specification allows a
    -- node to be identified by one host and served from another.
    base_url                varchar(255) NOT NULL,

    -- Where this node publishes the public keys its tokens are signed with, so
    -- that a peer can verify them without sharing a secret (RNF11).
    jwks_uri                varchar(255),

    -- The certificate fingerprint pinned for node-to-node mTLS, learned during
    -- discovery. Peers belong to different operators and share no root of
    -- trust, so the fingerprint is the trust anchor (RNF08).
    certificate_fingerprint varchar(128),

    -- Which row is this instance. Every node stores its own entry, because a
    -- user has to point at their origin server whether it is local or remote.
    is_local                boolean      NOT NULL DEFAULT false,

    discovered_at           timestamptz,

    -- Controls whether the node takes part in replication, without losing what
    -- discovery already learned about it.
    active                  boolean      NOT NULL DEFAULT true,

    CONSTRAINT servers_domain_key UNIQUE (domain),
    CONSTRAINT servers_domain_format CHECK (domain ~ '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$'),
    CONSTRAINT servers_base_url_scheme CHECK (base_url ~ '^https?://')
);

-- Exactly one row may claim to be this instance. Two would make the question
-- "is this user local" unanswerable, and that question decides whether this
-- node authenticates them or merely replicates them.
CREATE UNIQUE INDEX servers_local_key ON federation.servers (is_local) WHERE is_local;

-- The replication worker walks the nodes that take part; the inactive ones are
-- the majority in a large catalogue and are never scanned.
CREATE INDEX servers_active_idx ON federation.servers (active) WHERE active;

-- MER: usuario. The readers this instance knows (RF06, UC06).
--
-- Both kinds of user live here. The ones this node is the origin server of,
-- who carry credentials, and the ones it merely replicates for a peer, who do
-- not. What tells them apart is federation.servers.is_local on the row
-- origin_server_id points at, which is why that flag has to be reliable.
CREATE TABLE identity.users (
    id               uuid         PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The node responsible for authenticating this user. Each user belongs to
    -- exactly one (RN08); migrating to another origin server is a rewrite of
    -- this single column (RF17, UC16), which is why every other table
    -- references users.id rather than the federated identifier.
    origin_server_id uuid         NOT NULL REFERENCES federation.servers (id),

    -- The first half of the federated identifier @local_name:domain.
    local_name       varchar(64)  NOT NULL,

    display_name     varchar(120) NOT NULL,

    -- Null on a node that only replicates this user: the address is personal
    -- data and is deliberately kept out of the replicated set (RN09), and a
    -- replica authenticates nobody, so it has no use for a password either.
    -- The TCC declares both NOT NULL; see docs/mer-mapping.md.
    email            varchar(255),
    password_hash    varchar(255),

    created_at       timestamptz  NOT NULL DEFAULT now(),

    -- Stamped by the application, never by a trigger. On the replicable
    -- entities this column is not "when the row was written here" but the
    -- timestamp that travelled with the operation from the device that
    -- authored it, and a BEFORE UPDATE trigger would overwrite it with the
    -- local now() as the operation is applied — which would make an old write
    -- from a device that had been offline beat a newer local one. Users are
    -- not replicated today, but a schema where half the tables stamp
    -- themselves and half do not is a trap; this one is uniform.
    updated_at       timestamptz  NOT NULL DEFAULT now(),

    -- Narrow enough that the identifier survives being embedded in a URL, in a
    -- JWT subject and in a .well-known lookup without escaping.
    CONSTRAINT users_local_name_format CHECK (local_name ~ '^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$'),
    CONSTRAINT users_email_format CHECK (email IS NULL OR email ~ '^[^[:space:]@]+@[^[:space:]@]+$')
);

-- RN09, first half: the identifier is unique on the pair, not on the local
-- name. @anthony:quire-a.example and @anthony:quire-b.example are two people.
CREATE UNIQUE INDEX users_identifier_key ON identity.users (origin_server_id, local_name);

-- RN09, second half: the address is unique only within the origin server.
-- Folding case is what stops the same address from being registered twice with
-- different capitalization.
CREATE UNIQUE INDEX users_origin_email_key ON identity.users (origin_server_id, lower(email))
    WHERE email IS NOT NULL;

-- Replication asks this per peer: which users do I hold on your behalf.
CREATE INDEX users_origin_server_id_idx ON identity.users (origin_server_id);

-- MER: replica_usuario. Which nodes a user allows to hold a copy of their data
-- (RF16, UC15).
--
-- This table is the whole of the sovereignty claim in RN03: nothing leaves this
-- node for a peer that does not have an active row here.
CREATE TABLE federation.user_replicas (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid        NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    server_id        uuid        NOT NULL REFERENCES federation.servers (id) ON DELETE CASCADE,

    authorized_at    timestamptz NOT NULL DEFAULT now(),

    -- Whether the authorization covers the e-book files as well as the
    -- metadata. Granting metadata without the files is the cheap replica a
    -- reader is most likely to want on a node they do not own.
    replicates_files boolean     NOT NULL DEFAULT false,

    -- Revocation clears this rather than deleting the row: the record that the
    -- authorization once existed is what explains why a peer still holds data.
    active           boolean     NOT NULL DEFAULT true,

    CONSTRAINT user_replicas_pair_key UNIQUE (user_id, server_id)
);

-- What the replication worker asks per peer: whose data may I send you.
CREATE INDEX user_replicas_server_id_idx ON federation.user_replicas (server_id) WHERE active;

-- MER: dispositivo. Each device bound to a user's account (RF11, UC10).
--
-- The id is the identity a vector clock entry is keyed by, which is what
-- binding a device is for: an unregistered writer would introduce a clock entry
-- no node could resolve to anybody.
CREATE TABLE identity.devices (
    id             uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid         NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,

    name           varchar(120) NOT NULL,
    platform       varchar(40)  NOT NULL,

    -- The instant of the last completed synchronization, which is the cursor
    -- RN06 delivers deltas from.
    last_synced_at timestamptz,

    -- Unbinding a device clears this instead of deleting the row. Every
    -- operation the device ever authored is still keyed by its id, and a vector
    -- clock naming a device nobody can resolve cannot be explained to the user,
    -- audited, or checked against RN10.
    active         boolean      NOT NULL DEFAULT true,

    CONSTRAINT devices_name_not_blank CHECK (length(btrim(name)) > 0)
);

CREATE INDEX devices_user_id_idx ON identity.devices (user_id);

-- MER: token_acesso. The temporary credentials issued by an origin server
-- (RF07, RF08, RF09; UC07, UC08).
--
-- The access token itself is never stored: it is a JWT, verified by signature
-- against the published JWKS (RNF11). What is stored is the pair of credentials
-- that outlive a single call — the session refresh token and the password
-- recovery token — because revoking a device means revoking those.
CREATE TABLE identity.access_tokens (
    id         uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid         NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,

    -- Null for a password recovery token: recovery happens when the reader has
    -- lost access, possibly from a device that is not bound to the account yet.
    device_id  uuid         REFERENCES identity.devices (id) ON DELETE CASCADE,

    kind       varchar(20)  NOT NULL,

    -- The digest, never the token. A database dump then hands an attacker
    -- nothing that can be replayed.
    token_hash varchar(255) NOT NULL,

    expires_at timestamptz  NOT NULL,

    -- Covers both "already used" and "revoked": in either case the credential
    -- must not be honoured again.
    consumed   boolean      NOT NULL DEFAULT false,

    CONSTRAINT access_tokens_hash_key UNIQUE (token_hash),
    CONSTRAINT access_tokens_kind CHECK (kind IN ('session_refresh', 'password_recovery')),

    -- A session refresh token is what a specific device presents to stay signed
    -- in; one that named no device could not be revoked with that device.
    CONSTRAINT access_tokens_session_needs_device CHECK (
        kind <> 'session_refresh' OR device_id IS NOT NULL
    )
);

-- Presenting a token is a lookup by digest, and the unique constraint above
-- already serves it. These two serve the other two questions: revoke
-- everything for this device, and sweep what has expired.
CREATE INDEX access_tokens_device_id_idx ON identity.access_tokens (device_id) WHERE NOT consumed;
CREATE INDEX access_tokens_expires_at_idx ON identity.access_tokens (expires_at) WHERE NOT consumed;
