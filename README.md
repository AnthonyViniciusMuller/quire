# Quire

Federated, offline-first backend for e-book synchronization.

A *quire* is a bundle of folded sheets that, gathered with others, forms a book — which is
what this system does with servers: each node is self-contained, and together they form a
federation where a reader's library follows them across providers.

Quire is the backend for an undergraduate thesis (TCC) on **data sovereignty for personal
reading libraries**. A user is identified as `@local_name:server`, chooses which nodes replicate
their data, and can migrate to a different home server without the previous one cooperating.

## Status

Under active implementation. See [`docs/ROADMAP.md`](docs/ROADMAP.md) for the commit-by-commit
plan and current progress.

## What it does

- **Offline-first.** Devices read and write while disconnected; changes reconcile on reconnect.
- **CRDT reconciliation.** Vector clocks establish causality, per-field LWW registers resolve
  concurrent writes deterministically, and tombstones handle deletion — every node converges to
  the same state without a coordinator.
- **Federated.** Nodes discover each other over `/.well-known/quire/server`, authenticate with
  mutual TLS, and replicate only the users who authorized them.
- **Portable identity.** A user can move their home server and keep their library, annotations,
  and reading positions.

## Architecture

The node server (`quired`) exposes two listeners:

| Port | Protocol | Surface |
|------|----------|---------|
| `9090` | gRPC | `AuthService`, `LibraryService`, `ReadingService`, `SyncService`, `FederationService` |
| `8080` | HTTP | `/.well-known/*` discovery (RFC 8615), JWKS, `/healthz`, `/readyz`, `/metrics` |

The code is organized hexagonally by feature slice — `identity`, `federation`, `library`,
`reading`, `sync` — each with its own `domain`, `application`, `infra` and `di` packages, over a
shared core in `internal/shared`. One package per entity and one per use case, following
[`College-Redberry/open-adoption`](https://github.com/College-Redberry/open-adoption); the
layout and the places this project departs from it are in
[`docs/architecture.md`](docs/architecture.md). E-book files live in S3-compatible object
storage, deduplicated by content hash; everything else lives in PostgreSQL, one schema per
slice.

A reference CLI client (`quirectl`) drives the end-to-end suites and stands in for the mobile
client during demonstrations.

```
cmd/quired      node server            internal/shared    config · errs · logging · crdt · persist · grpcx
cmd/quirectl    reference client       internal/<slice>   domain · application · infra · di
proto/quire/v1  network contracts      migrations/        golang-migrate
deploy/docker   local federation       deploy/k8s         kustomize + Istio + cert-manager
```

## Getting started

```bash
make dev-up            # docker compose: two federated nodes, each with Postgres and MinIO
make test              # unit tests
make test-up           # throwaway Postgres and MinIO for the integration suite
make test-integration  # integration tests against them
make test-e2e          # end-to-end against the two nodes
```

`make dev-up` brings up two nodes that know nothing about each other until a reader says
so. Inside the compose network they are `quire-a.example` and `quire-b.example`, which is
what they discover each other by; from the host they answer on `127.0.0.1:19090` and
`127.0.0.1:29090` for gRPC, on `127.0.0.1:18080` and `127.0.0.1:28080` for the discovery
documents, and on `127.0.0.1:18090` and `127.0.0.1:28090` for gRPC-Web — the lane a browser
uses, because it cannot speak gRPC itself. Those two are plain HTTP, which a browser accepts
on a loopback origin without a certificate. They present the self-signed certificates `make dev-certs` generates, so a client
outside the network names one:

```bash
quirectl --server 127.0.0.1:19090 --ca deploy/docker/certs/quire-a.example.crt.pem whoami
```

Full target list in the [`Makefile`](Makefile); deployment instructions in
[`docs/deployment.md`](docs/deployment.md).

## License

[MIT](LICENSE).
