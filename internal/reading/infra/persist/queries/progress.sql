-- Where each device stopped in each work (RF02, RN01; UC05).
--
-- One row per work and device, which progress_ebook_device_key enforces and
-- C05 in docs/tcc-corrections.md is about. The pair is how every statement
-- addresses the row: the identifier exists to be what a synchronization
-- operation names, and nothing here looks a position up by it.
--
-- There is no statement that removes a row and none that tombstones one. A
-- reader who stops reading a work leaves their position where it was, and the
-- row goes when the work goes, by the cascade the schema declares.
--
-- Two replication columns rather than four, and that is the whole of C05 in
-- the schema: the row has one writer, so the device whose write it reflects is
-- already its own key and there is no tie to break. The clock is a version
-- counter for deduplication during replication, not a conflict resolver.

-- name: CreateProgress :exec
INSERT INTO reading.progress (id, ebook_id, device_id, locator, percent, vector_clock, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- Where the device has reached now, and the version the write stamped.
--
-- Neither half of the natural key is in the SET list. A position belongs to
-- one work and one device for as long as it exists, and a statement that could
-- move it to another would be a statement that could move another device's
-- bookmark.
-- name: UpdateProgress :execrows
UPDATE reading.progress
SET locator      = $2,
    percent      = $3,
    vector_clock = $4,
    updated_at   = $5
WHERE id = $1;

-- Where one device stopped in one work. No row is a work that device has never
-- opened, which the caller answers by recording a first position rather than
-- by failing.
-- name: GetProgressByPair :one
SELECT id, ebook_id, device_id, locator, percent, vector_clock, updated_at
FROM reading.progress
WHERE ebook_id = $1
  AND device_id = $2;

-- Every device's position in one work (RN01), which is what a client needs in
-- order to decide what to show the reader: the furthest position, the most
-- recent one, or a prompt asking which to resume from. That decision belongs
-- to the client, so this does not make it by returning one row.
--
-- It is not paginated, and the bound is the reader's own: there is one row per
-- appliance they have ever read the work on. The ordering is by device so that
-- two calls return the same list in the same order, which a client diffing
-- against what it already showed depends on.
--
-- progress_ebook_device_key serves it: the pair is unique, so the constraint's
-- index leads with the work and is already in device order within it.
-- name: ListProgressForEbook :many
SELECT id, ebook_id, device_id, locator, percent, vector_clock, updated_at
FROM reading.progress
WHERE ebook_id = $1
ORDER BY device_id;
