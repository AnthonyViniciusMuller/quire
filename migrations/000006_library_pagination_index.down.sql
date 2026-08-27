DROP INDEX IF EXISTS library.ebooks_user_id_imported_at_idx;

CREATE INDEX ebooks_user_id_idx ON library.ebooks (user_id) WHERE NOT deleted;
