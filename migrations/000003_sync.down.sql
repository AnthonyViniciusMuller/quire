-- The constraint on identity.devices is created by the up migration of this
-- schema, so it is dropped here rather than left behind by the schema drop.
DROP SCHEMA IF EXISTS sync CASCADE;

ALTER TABLE identity.devices DROP CONSTRAINT IF EXISTS devices_id_user_id_key;
