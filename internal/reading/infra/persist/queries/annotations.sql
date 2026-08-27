-- What a reader has written in a work (RF03, UC04).
--
-- Nothing here is scoped to a reader, and unlike the library slice that is not
-- an omission the statements make up for: reading.annotations references the
-- work and not the reader, so who a mark belongs to is a question about the
-- work it is in. Every use case asks that question first, of the library
-- slice's repository, and only then addresses a mark — which is why the
-- statements below take an identifier and a work rather than an identifier and
-- a reader.
--
-- The four replication columns are written by the application and never by a
-- trigger, for the reason C01 gives: a trigger would overwrite the authoring
-- device's timestamp with the local now() as the operation was applied, and an
-- old write from a device that had been offline would beat a newer local one.

-- name: CreateAnnotation :exec
INSERT INTO reading.annotations (id, ebook_id, device_id, kind, text, locator, vector_clock,
                                 updated_at, deleted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- Everything UC04 edits, and the revision the write stamped.
--
-- The tombstone is written here rather than by a statement of its own, because
-- a deletion is a write like any other: it carries a vector clock, a timestamp
-- and the device that made it, and a DELETE would carry none of the three —
-- the mark would be resurrected by the next node that had not yet heard about
-- it.
--
-- The work is not in the SET list. A mark is made in a work and stays in it; a
-- note that moved between books would be a note about a passage that is not
-- there.
-- name: UpdateAnnotation :execrows
UPDATE reading.annotations
SET device_id    = $2,
    kind         = $3,
    text         = $4,
    locator      = $5,
    vector_clock = $6,
    updated_at   = $7,
    deleted      = $8
WHERE id = $1;

-- Tombstoned or not, because the caller is the one that knows what it is for:
-- a reader asking to see the mark is answered that there is none, and a reader
-- asking to delete it again is answered that it is already gone.
-- name: GetAnnotationByID :one
SELECT id, ebook_id, device_id, kind, text, locator, vector_clock, updated_at, deleted
FROM reading.annotations
WHERE id = $1;

-- One page of what was written in one work, ordered by the identifier.
--
-- The identifier is the whole of the ordering and the whole of the keyset, and
-- it is the only column that could be either. Quadro 22 gives an annotation no
-- creation instant, and updated_at is rewritten by every edit, so a page
-- ordered by either would skip or repeat a mark that a second device edited
-- between two requests — which is the failure keyset pagination exists to
-- remove, arriving through the sort key instead of through the offset.
--
-- The order is therefore stable rather than meaningful. Where a mark sits in a
-- book is a property of the document, which the client can resolve and this
-- node cannot; what the statement guarantees is that a client walking every
-- page sees every mark exactly once, which is what it needs in order to sort
-- them itself.
--
-- annotations_ebook_id_id_idx serves the whole statement: the work selects,
-- the ordering is the index order, and the cursor is a seek into it.
-- name: ListAnnotations :many
SELECT id, ebook_id, device_id, kind, text, locator, vector_clock, updated_at, deleted
FROM reading.annotations
WHERE ebook_id = sqlc.arg(ebook_id)
  AND NOT deleted
  AND (NOT sqlc.arg(after_cursor)::boolean OR id > sqlc.arg(cursor_id)::uuid)
ORDER BY id
LIMIT sqlc.arg(page_size)::integer;
