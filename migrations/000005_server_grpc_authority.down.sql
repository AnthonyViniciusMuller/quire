ALTER TABLE federation.servers
    DROP CONSTRAINT IF EXISTS servers_grpc_authority_format;

ALTER TABLE federation.servers
    DROP COLUMN IF EXISTS grpc_authority;
