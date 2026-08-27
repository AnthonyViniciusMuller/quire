-- The files this node actually holds, keyed by the digest of each (D02 in
-- docs/tcc-corrections.md — an extension to the MER, which models only the
-- metadata).
--
-- Nothing here is scoped to a reader, and that is the point rather than an
-- omission: the table is keyed by the digest, so two readers who imported the
-- same file and one reader who imported it on two devices converge on one
-- object. What is per-reader is library.ebooks, which names the digest and
-- does not reference this table — a node replicating a reader without their
-- files has every work row and none of these.
--
-- There is no statement that removes a row. A file two readers hold is a file
-- one of them deleting must not take from the other, so reclaiming an object
-- means asking whether any work still names the digest — which is a sweep, not
-- a cascade, and belongs to an operator's job rather than to a reader's call.

-- name: CreateContent :exec
INSERT INTO library.ebook_contents (content_hash, size_bytes, media_type, storage_bucket,
                                    storage_key, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetContentByHash :one
SELECT content_hash, size_bytes, media_type, storage_bucket, storage_key, created_at
FROM library.ebook_contents
WHERE content_hash = $1;

-- Whether this node holds the bytes, which is what a creation is answered
-- with. It is a separate statement from the read above because the answer is a
-- boolean the caller acts on rather than a row it renders, and asking the
-- primary key for its own existence is an index-only lookup.
-- name: HasContent :one
SELECT EXISTS (SELECT 1 FROM library.ebook_contents WHERE content_hash = $1);
