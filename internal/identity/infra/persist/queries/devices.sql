-- The appliances bound to a reader's account (RF11, UC10).
--
-- Nothing here deletes. Unbinding a device clears identity.devices.active,
-- because every operation the device authored still names its id and a vector
-- clock entry pointing at a device nobody can resolve cannot be explained to
-- the reader, audited, or checked against RN10.

-- name: CreateDevice :exec
INSERT INTO identity.devices (id, user_id, name, platform, last_synced_at, active)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateDevice :execrows
UPDATE identity.devices
SET name           = $2,
    last_synced_at = $3,
    active         = $4
WHERE id = $1;

-- name: GetDeviceByID :one
SELECT id, user_id, name, platform, last_synced_at, active
FROM identity.devices
WHERE id = $1;

-- Ordered by name, so that the list RF11 makes auditable does not reshuffle
-- between two calls; the id breaks the tie, since two appliances may well be
-- called the same thing. Unbound devices are hidden unless asked for: their
-- rows survive revocation, and they are what explains a clock entry the reader
-- no longer recognizes.
-- name: ListDevicesByUser :many
SELECT id, user_id, name, platform, last_synced_at, active
FROM identity.devices
WHERE user_id = $1
  AND (active OR sqlc.arg(include_inactive)::boolean)
ORDER BY name, id;
