-- Rename identity.access_tokens to identity.credentials (C09 in
-- docs/tcc-corrections.md).
--
-- The MER calls the entity token_acesso, and 000001 followed that name to stay
-- faithful to Appendix A. Its own description, however, states that the access
-- token is not persisted at all — it is a JWT, verified by signature (RNF11).
-- What the table holds is the session refresh credential and the password
-- recovery credential, which is what the name now says.
--
-- This lands as its own migration rather than as an edit to 000001, so that a
-- database which already ran 000001 has a path forward. The comment in 000001
-- is left describing the entity under its old name, as the historical record of
-- what that migration did.

ALTER TABLE identity.access_tokens RENAME TO credentials;

-- Renaming a table renames none of the objects on it. The unique constraint in
-- particular has to follow: a repository tells one uniqueness failure from
-- another by the constraint name that comes back in the driver error, so a
-- constraint still called access_tokens_hash_key would silently stop matching.
ALTER TABLE identity.credentials RENAME CONSTRAINT access_tokens_pkey TO credentials_pkey;
ALTER TABLE identity.credentials RENAME CONSTRAINT access_tokens_hash_key TO credentials_hash_key;
ALTER TABLE identity.credentials RENAME CONSTRAINT access_tokens_kind TO credentials_kind;
ALTER TABLE identity.credentials
    RENAME CONSTRAINT access_tokens_session_needs_device TO credentials_session_needs_device;
ALTER TABLE identity.credentials RENAME CONSTRAINT access_tokens_user_id_fkey TO credentials_user_id_fkey;
ALTER TABLE identity.credentials RENAME CONSTRAINT access_tokens_device_id_fkey TO credentials_device_id_fkey;

ALTER INDEX identity.access_tokens_device_id_idx RENAME TO credentials_device_id_idx;
ALTER INDEX identity.access_tokens_expires_at_idx RENAME TO credentials_expires_at_idx;

-- The NOT NULL constraints are deliberately left alone. PostgreSQL 18 gives
-- them generated names of its own and earlier versions give them none, so
-- renaming them would make this migration fail on anything below 18. Nothing
-- refers to them by name.
