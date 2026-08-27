-- The index a page of what a reader wrote in a work is read through.
--
-- Listing is keyset paginated, ordered by the identifier, and the index the
-- reading schema shipped with covers only ebook_id. That was enough to find
-- the marks in a work and not enough to return them in order: every page
-- sorted every mark in the book and then discarded all but fifty, so the cost
-- of the last page grew with the number of notes rather than staying flat —
-- which is the property keyset pagination exists to give.
--
-- The identifier is the ordering because it is the only column that can be.
-- Quadro 22 gives an annotation no creation instant, and updated_at is
-- rewritten by every edit, so an index on either would order a page by a value
-- that moves while the page is being turned.
--
-- The composite covers everything the single-column index covered, since
-- ebook_id is its leading column, so the old one is dropped rather than kept
-- beside it: a redundant index is written on every insert and read by nothing.
--
-- The partial clause is the same one: tombstoned marks are never listed, and
-- excluding them keeps the index the size of what the reader has written
-- rather than the size of everything they ever wrote.
DROP INDEX IF EXISTS reading.annotations_ebook_id_idx;

CREATE INDEX annotations_ebook_id_id_idx
    ON reading.annotations (ebook_id, id)
    WHERE NOT deleted;
