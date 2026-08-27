-- The works in a reader's collection (RF01, RF04; UC01, UC02).
--
-- Every statement here is scoped to one reader, and the ones that are not
-- carry the identifier the caller already read the reader out of. There is no
-- call in this slice that reads across readers: a work that belongs to
-- somebody else is answered exactly as one that does not exist, and a
-- statement that could return it is a statement that could be asked to.
--
-- Two groups of columns never move together. The description — title, author,
-- publisher, language, extra_metadata — is what UC01 edits, and the file —
-- format, content_hash, size_bytes — is fixed at import, because a row whose
-- digest changed would describe a file it is not. Only the first group appears
-- in the update below.
--
-- The four replication columns are written by the application and never by a
-- trigger. C01 in docs/tcc-corrections.md is why: a trigger would overwrite
-- the authoring device's timestamp with the local now() as the operation was
-- applied, and an old write from a device that had been offline would beat a
-- newer local one.

-- name: CreateEbook :exec
INSERT INTO library.ebooks (id, user_id, title, author, publisher, language, format, content_hash,
                            size_bytes, extra_metadata, imported_at, vector_clock, updated_at,
                            device_id, deleted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- What UC01 edits, and the revision the write stamped.
--
-- The tombstone is written here rather than by a statement of its own, because
-- a deletion is a write like any other: it carries a vector clock, a timestamp
-- and the device that made it, and a DELETE would carry none of the three —
-- the row would be resurrected by the next node that had not yet heard about
-- it.
-- name: UpdateEbook :execrows
UPDATE library.ebooks
SET title          = $2,
    author         = $3,
    publisher      = $4,
    language       = $5,
    extra_metadata = $6,
    vector_clock   = $7,
    updated_at     = $8,
    device_id      = $9,
    deleted        = $10
WHERE id = $1;

-- Tombstoned or not, because the caller is the one that knows what it is for:
-- a reader asking to see the work is answered that there is none, and a reader
-- asking to delete it again is answered that it is already gone.
-- name: GetEbookByID :one
SELECT id, user_id, title, author, publisher, language, format, content_hash, size_bytes,
       extra_metadata, imported_at, vector_clock, updated_at, device_id, deleted
FROM library.ebooks
WHERE id = $1;

-- One page of a reader's collection, most recently imported first.
--
-- The pagination is keyset and not offset, and the difference shows on a
-- collection being written to while it is being read: an offset skips a work
-- whenever an earlier one is imported between two pages, while this resumes
-- from a row whose neighbour the client has already seen. The row comparison
-- is what makes the resume exact — imported_at is not unique, two works
-- imported in the same microsecond are ordinary, and the identifier breaks
-- that tie in the same direction the ordering does.
--
-- Both filters are optional and both are expressed as a flag beside the value
-- rather than as a NULL. A NULL argument would make the comparison NULL rather
-- than false, which is the same as no filter only by accident; the flag says
-- what is meant.
--
-- The collection filter is EXISTS and not a join, so that a work filed twice
-- under the same grouping could not appear twice in a page — which the pair
-- constraint of C06 already prevents, and which the query should not depend on
-- it for.
--
-- ebooks_user_id_imported_at_idx serves the whole statement: the reader
-- selects, the ordering is the index order, and the cursor is a seek into it.
-- name: ListEbooks :many
SELECT id, user_id, title, author, publisher, language, format, content_hash, size_bytes,
       extra_metadata, imported_at, vector_clock, updated_at, device_id, deleted
FROM library.ebooks
WHERE user_id = sqlc.arg(user_id)
  AND NOT deleted
  AND (NOT sqlc.arg(in_collection)::boolean
    OR EXISTS (SELECT 1
               FROM library.ebook_collections
               WHERE library.ebook_collections.ebook_id = library.ebooks.id
                 AND library.ebook_collections.collection_id = sqlc.arg(collection_id)
                 AND NOT library.ebook_collections.deleted))
  AND (NOT sqlc.arg(after_cursor)::boolean
    OR (imported_at, id) < (sqlc.arg(cursor_imported_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY imported_at DESC, id DESC
LIMIT sqlc.arg(page_size)::integer;
