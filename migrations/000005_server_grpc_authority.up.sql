-- The authority a peer dials for gRPC, learned during discovery (D06 in
-- docs/tcc-corrections.md).
--
-- The MER gives servidor a single url_base, "o endpoint efetivo obtido pelo
-- descobrimento". In the Kubernetes deployment that is enough: one Istio
-- gateway answers for the domain on 443 and separates gRPC from HTTP by ALPN,
-- so both endpoints share an authority and one column addresses both.
--
-- They are not the same everywhere. The .well-known documents are plain HTTP
-- because RFC 8615 requires it, and the API is gRPC; in the two-node docker
-- compose federation, and in any deployment without a mesh in front, the two
-- listen on different ports. A peer that learned only url_base would have
-- nowhere to dial for replication.
ALTER TABLE federation.servers
    ADD COLUMN grpc_authority varchar(255);

-- Nullable, and it has to be. A node discovered before this column existed has
-- none, and so does a peer whose document publishes no grpc key — which is a
-- node this instance can record and cannot replicate to. Refusing it here
-- would turn a peer that is merely unreachable into a peer that cannot be
-- described.
--
-- The format is host:port rather than a URL, because that is what a gRPC
-- client dials, and the port is required: the whole reason the column exists
-- is that it is not the one the base URL implies.
ALTER TABLE federation.servers
    ADD CONSTRAINT servers_grpc_authority_format CHECK (
        grpc_authority IS NULL
            OR grpc_authority ~ '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?:[0-9]{1,5}$'
    );
