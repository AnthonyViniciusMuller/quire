-- Back to the index the reading schema shipped with.
DROP INDEX IF EXISTS reading.annotations_ebook_id_id_idx;

CREATE INDEX annotations_ebook_id_idx
    ON reading.annotations (ebook_id)
    WHERE NOT deleted;
