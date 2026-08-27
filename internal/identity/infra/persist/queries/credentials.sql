-- The temporary credentials this node issues (RF07, RF08, RF09; UC07, UC08).
--
-- Only digests are stored, never the credential itself, so every statement here
-- addresses a row by the digest of what a caller presented or by a key learned
-- from it. A dump of this table then hands an attacker nothing to replay.

-- name: CreateCredential :exec
INSERT INTO identity.credentials (id, user_id, device_id, kind, token_hash, expires_at, consumed)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetCredentialByTokenHash :one
SELECT id, user_id, device_id, kind, token_hash, expires_at, consumed
FROM identity.credentials
WHERE token_hash = $1;

-- The check and the write are one statement, and the NOT consumed in the WHERE
-- clause is what makes it one. Two devices presenting the same refresh
-- credential at the same instant both find it unconsumed if they read first and
-- write after, and both are answered with a session; here the second UPDATE
-- matches no row, reports zero, and the caller refuses it.
-- name: ConsumeCredential :execrows
UPDATE identity.credentials
SET consumed = true
WHERE id = $1
  AND NOT consumed;

-- What unbinding a device means for the sessions it holds.
-- name: ConsumeCredentialsForDevice :execrows
UPDATE identity.credentials
SET consumed = true
WHERE device_id = $1
  AND NOT consumed;

-- What resetting a password means for every session of every device: the reader
-- is recovering precisely because they may not be the only party holding the
-- old one.
-- name: ConsumeCredentialsForUser :execrows
UPDATE identity.credentials
SET consumed = true
WHERE user_id = $1
  AND kind = $2
  AND NOT consumed;

-- Housekeeping, and nothing depends on it running: an expired credential is
-- refused whether or not its row is still there.
-- name: DeleteExpiredCredentials :execrows
DELETE FROM identity.credentials
WHERE expires_at < $1;
