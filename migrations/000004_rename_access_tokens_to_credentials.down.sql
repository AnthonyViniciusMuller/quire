ALTER INDEX identity.credentials_expires_at_idx RENAME TO access_tokens_expires_at_idx;
ALTER INDEX identity.credentials_device_id_idx RENAME TO access_tokens_device_id_idx;

ALTER TABLE identity.credentials RENAME CONSTRAINT credentials_device_id_fkey TO access_tokens_device_id_fkey;
ALTER TABLE identity.credentials RENAME CONSTRAINT credentials_user_id_fkey TO access_tokens_user_id_fkey;
ALTER TABLE identity.credentials
    RENAME CONSTRAINT credentials_session_needs_device TO access_tokens_session_needs_device;
ALTER TABLE identity.credentials RENAME CONSTRAINT credentials_kind TO access_tokens_kind;
ALTER TABLE identity.credentials RENAME CONSTRAINT credentials_hash_key TO access_tokens_hash_key;
ALTER TABLE identity.credentials RENAME CONSTRAINT credentials_pkey TO access_tokens_pkey;

ALTER TABLE identity.credentials RENAME TO access_tokens;
