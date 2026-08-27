-- The index a page of a reader's collection is read through.
--
-- Listing is keyset paginated, ordered by (imported_at, id) descending, and
-- the index the library schema shipped with covers only user_id. That was
-- enough to find a reader's rows and not enough to return them in order: every
-- page sorted the whole collection and then discarded all but fifty rows, so
-- the cost of the last page grew with the size of the library rather than
-- staying flat — which is the property keyset pagination exists to give.
--
-- The composite covers everything the single-column index covered, since
-- user_id is its leading column, so the old one is dropped rather than kept
-- beside it: a redundant index is written on every insert and read by nothing.
--
-- The partial clause is the same one: tombstoned works are never listed, and
-- excluding them keeps the index the size of the collection rather than the
-- size of its history.
DROP INDEX IF EXISTS library.ebooks_user_id_idx;

CREATE INDEX ebooks_user_id_imported_at_idx
    ON library.ebooks (user_id, imported_at DESC, id DESC)
    WHERE NOT deleted;
