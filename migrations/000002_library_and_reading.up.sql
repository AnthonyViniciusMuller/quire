-- Library and reading: the e-books a reader owns, how they group them, and
-- what they wrote and where they stopped.
--
-- MER: ebook, colecao, ebook_colecao, progresso_leitura, anotacao
-- (subsection 4.2.4 and Appendix A of the TCC). The Portuguese-to-schema name
-- mapping is in docs/mer-mapping.md.
--
-- Everything here replicates. That is what separates these two schemas from
-- identity and federation, and it is why every table below carries the same
-- four columns: vector_clock, updated_at, device_id and, where deletion is
-- possible, deleted. Together they are what lets two nodes that saw the same
-- writes in different orders arrive at the same rows.
--
--   vector_clock   the causal version of the row, the join semilattice that
--                  internal/shared/crdt proves the merge laws over.
--   updated_at     the last-writer-wins tie-break, for the concurrent case the
--                  vector clock cannot order. See C01 in docs/tcc-corrections.md:
--                  it has to be a hybrid logical clock, not a wall clock, and
--                  the application stamps it — never a trigger, which would
--                  overwrite the authoring device's timestamp with the local
--                  now() as the operation is applied and make an old write from
--                  a device that had been offline beat a newer local one.
--   device_id      the device whose write the row currently reflects, which is
--                  the second half of that tie-break. Appendix A omits it from
--                  Quadros 18, 19 and 20; see C02.
--   deleted        the tombstone. A row removed outright is resurrected by the
--                  next node that had not yet heard about the deletion.

CREATE SCHEMA IF NOT EXISTS library;
CREATE SCHEMA IF NOT EXISTS reading;

-- The bytes of an e-book this node actually holds, keyed by the digest of the
-- file (D02 in docs/tcc-corrections.md — an extension to the MER, which models
-- only the metadata).
--
-- It is deliberately not referenced by library.ebooks. A node that replicates a
-- user with replicates_files false holds every e-book row and none of the
-- files, so a foreign key would make the metadata unreplicable there. The
-- presence of a row here means one thing only: this node has the bytes.
--
-- Keying by digest is also what deduplicates. Two readers who imported the same
-- file, and the same reader importing it on two devices, converge on one object.
CREATE TABLE library.ebook_contents (
    content_hash   varchar(64)  PRIMARY KEY,
    size_bytes     bigint       NOT NULL,
    media_type     varchar(100) NOT NULL,

    -- Where the object lives. The bucket is recorded alongside the key so that
    -- a node can be pointed at a different bucket without rewriting the rows
    -- that already exist.
    storage_bucket varchar(255) NOT NULL,
    storage_key    varchar(512) NOT NULL,

    created_at     timestamptz  NOT NULL DEFAULT now(),

    CONSTRAINT ebook_contents_hash_format CHECK (content_hash ~ '^[a-f0-9]{64}$'),
    CONSTRAINT ebook_contents_size_positive CHECK (size_bytes > 0)
);

-- MER: ebook. The items of a reader's collection (RF01, RF04; UC01, UC02).
CREATE TABLE library.ebooks (
    id             uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid         NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,

    title          varchar(255) NOT NULL,
    author         varchar(255),
    publisher      varchar(255),
    language       varchar(20),
    format         varchar(10)  NOT NULL,

    -- The digest of the file, used for integrity and for recognizing the same
    -- work imported twice. It is not a foreign key; see library.ebook_contents.
    content_hash   varchar(64)  NOT NULL,
    size_bytes     bigint,

    -- Room for the metadata a format carries and this schema does not name,
    -- without a migration for each one (RF05).
    extra_metadata jsonb,

    imported_at    timestamptz  NOT NULL DEFAULT now(),

    vector_clock   jsonb        NOT NULL DEFAULT '{}',
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    device_id      uuid         REFERENCES identity.devices (id) ON DELETE SET NULL,
    deleted        boolean      NOT NULL DEFAULT false,

    CONSTRAINT ebooks_title_not_blank CHECK (length(btrim(title)) > 0),
    CONSTRAINT ebooks_hash_format CHECK (content_hash ~ '^[a-f0-9]{64}$'),
    CONSTRAINT ebooks_size_positive CHECK (size_bytes IS NULL OR size_bytes > 0),
    CONSTRAINT ebooks_metadata_is_object CHECK (
        extra_metadata IS NULL OR jsonb_typeof(extra_metadata) = 'object'
    ),
    CONSTRAINT ebooks_clock_is_object CHECK (jsonb_typeof(vector_clock) = 'object')
);

-- Listing a reader's library, which is the query the client makes most.
CREATE INDEX ebooks_user_id_idx ON library.ebooks (user_id) WHERE NOT deleted;

-- Answering "do I already hold this file", on import and on replication.
CREATE INDEX ebooks_content_hash_idx ON library.ebooks (content_hash);

-- MER: colecao. The groupings a reader defines over their library (RF05, UC03).
CREATE TABLE library.collections (
    id           uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid         NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,

    name         varchar(120) NOT NULL,

    -- Collections and categories are the same structure with a different
    -- meaning, which is what lets RF05 offer both without a second entity.
    kind         varchar(20)  NOT NULL DEFAULT 'collection',

    description  text,
    created_at   timestamptz  NOT NULL DEFAULT now(),

    vector_clock jsonb        NOT NULL DEFAULT '{}',
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    device_id    uuid         REFERENCES identity.devices (id) ON DELETE SET NULL,
    deleted      boolean      NOT NULL DEFAULT false,

    CONSTRAINT collections_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT collections_kind CHECK (kind IN ('collection', 'category')),
    CONSTRAINT collections_clock_is_object CHECK (jsonb_typeof(vector_clock) = 'object')
);

CREATE INDEX collections_user_id_idx ON library.collections (user_id) WHERE NOT deleted;

-- MER: ebook_colecao. Which works belong to which grouping.
--
-- It replicates like everything else here, so membership is an
-- last-writer-wins register per pair rather than a plain row: two devices that
-- add and remove the same book from the same shelf while offline have to reach
-- the same answer.
CREATE TABLE library.ebook_collections (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    ebook_id      uuid        NOT NULL REFERENCES library.ebooks (id) ON DELETE CASCADE,
    collection_id uuid        NOT NULL REFERENCES library.collections (id) ON DELETE CASCADE,

    vector_clock  jsonb       NOT NULL DEFAULT '{}',
    updated_at    timestamptz NOT NULL DEFAULT now(),
    device_id     uuid        REFERENCES identity.devices (id) ON DELETE SET NULL,
    deleted       boolean     NOT NULL DEFAULT false,

    -- C06: Quadro 20 has no uniqueness constraint, so nothing stops the same
    -- work from being filed twice in the same grouping — which is exactly what
    -- two offline devices will do. The pair is the natural key; the row is
    -- reused, and its tombstone flipped, rather than inserted again.
    CONSTRAINT ebook_collections_pair_key UNIQUE (ebook_id, collection_id),
    CONSTRAINT ebook_collections_clock_is_object CHECK (jsonb_typeof(vector_clock) = 'object')
);

-- Reading a shelf: which works are on it.
CREATE INDEX ebook_collections_collection_id_idx ON library.ebook_collections (collection_id)
    WHERE NOT deleted;

-- MER: progresso_leitura. Where each device stopped in each work (RF02, RN01;
-- UC05).
--
-- One row per work and device, as Quadro 21 specifies. That grain has a
-- consequence worth stating rather than inferring (C05): a row has exactly one
-- writer, its writes are totally ordered by that device's own counter, and two
-- versions of it can therefore never be concurrent. Reading progress is
-- conflict-free by construction, and the vector clock here is a version counter
-- for deduplication during replication rather than a conflict resolver.
--
-- Which position to show the reader — the furthest, the most recent — is the
-- client's decision, and it needs every device's row to make it.
CREATE TABLE reading.progress (
    id           uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    ebook_id     uuid          NOT NULL REFERENCES library.ebooks (id) ON DELETE CASCADE,
    device_id    uuid          NOT NULL REFERENCES identity.devices (id) ON DELETE CASCADE,

    -- The position in the document, expressed so that it survives the format:
    -- a CFI in an EPUB, a page in a PDF.
    locator      varchar(255)  NOT NULL,
    percent      numeric(5, 2),

    vector_clock jsonb         NOT NULL DEFAULT '{}',
    updated_at   timestamptz   NOT NULL DEFAULT now(),

    -- C05: without this the rows accumulate instead of being updated, and
    -- "where this device stopped in this book" stops having one answer.
    CONSTRAINT progress_ebook_device_key UNIQUE (ebook_id, device_id),
    CONSTRAINT progress_locator_not_blank CHECK (length(btrim(locator)) > 0),
    CONSTRAINT progress_percent_range CHECK (percent IS NULL OR (percent >= 0 AND percent <= 100)),
    CONSTRAINT progress_clock_is_object CHECK (jsonb_typeof(vector_clock) = 'object')
);

-- MER: anotacao. What the reader wrote, and where (RF03, UC04).
CREATE TABLE reading.annotations (
    id           uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    ebook_id     uuid         NOT NULL REFERENCES library.ebooks (id) ON DELETE CASCADE,

    -- The device whose write this row reflects, not the one that first created
    -- it. Quadro 22 describes it as the originating device; the model never
    -- uses that, while the last-writer-wins tie-break requires this. See C10.
    device_id    uuid         REFERENCES identity.devices (id) ON DELETE SET NULL,

    kind         varchar(20)  NOT NULL,
    text         text,

    -- The passage the annotation refers to, in the same format-independent
    -- form as a reading position.
    locator      varchar(255) NOT NULL,

    vector_clock jsonb        NOT NULL DEFAULT '{}',
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    deleted      boolean      NOT NULL DEFAULT false,

    CONSTRAINT annotations_kind CHECK (kind IN ('note', 'highlight', 'bookmark')),
    CONSTRAINT annotations_locator_not_blank CHECK (length(btrim(locator)) > 0),

    -- A note with no text is an empty note; a highlight and a bookmark are
    -- about the passage, and carry text only when the reader added one.
    CONSTRAINT annotations_note_has_text CHECK (kind <> 'note' OR length(btrim(coalesce(text, ''))) > 0),
    CONSTRAINT annotations_clock_is_object CHECK (jsonb_typeof(vector_clock) = 'object')
);

-- Opening a book: everything written in it.
CREATE INDEX annotations_ebook_id_idx ON reading.annotations (ebook_id) WHERE NOT deleted;
