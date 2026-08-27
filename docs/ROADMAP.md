# Implementation roadmap

One checkbox per commit. Tick a box only after the commit lands, so that any future session can
resume the work by reading this file and `git log`.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
and are written in English. Commits stay small and are reviewed before landing.

The layout every slice follows — one package per entity, one per use case — is in
[`architecture.md`](architecture.md), together with the places this project departs from the
reference implementation it is modelled on. Read it before starting a slice.

Thesis traceability: requirement identifiers (RF, RNF, RN, UC) refer to the TCC specification;
the entity-name mapping between the MER and the database schema lives in
[`mer-mapping.md`](mer-mapping.md), and everything the implementation found that has to reach
the thesis — corrections to the specification and deliberate divergences from it — lives in
[`tcc-corrections.md`](tcc-corrections.md). Append there when you find another one.

## Phase 0 — Repository bootstrap

- [x] `chore: initialize go module and repository layout`
- [x] `docs: add readme and license`
- [x] `docs: add implementation roadmap`
- [x] `build: add makefile with development targets`
- [x] `chore: add golangci-lint configuration`
- [x] `ci: add github actions workflow for lint and unit tests`

## Phase 1 — Shared core

- [x] `feat: add configuration loading from environment`
- [x] `feat: add domain error type with wrapping`
- [x] `feat: add structured logging setup`
- [x] `feat: add vector clock comparison primitives`
- [x] `test: add property tests for crdt merge laws`

## Phase 2 — Persistence

- [x] `feat: add postgres connection pool and transaction helper`
- [x] `feat: add identity and federation schema migrations`
- [x] `docs: add tcc correction log`
- [x] `feat: add library and reading schema migrations`
- [x] `feat: add sync schema migrations`
- [x] `build: add sqlc configuration and generation target`
- [x] `docs: add mer to schema mapping table`
- [x] `refactor: rename the access token entity to credential`
- [x] `docs: settle the hybrid logical clock decision`

## Phase 3 — Protobuf contracts

- [x] `build: add buf configuration for protobuf generation`
- [x] `feat: add shared protobuf messages and vector clock type`
- [x] `feat: add auth service protobuf definition`
- [x] `feat: add library service protobuf definition`
- [x] `feat: add reading service protobuf definition`
- [x] `feat: add sync service protobuf definition`
- [x] `docs: record what a home server migration has to carry`
- [x] `feat: add federation service protobuf definition`

## Phase 4 — Servers

- [x] `feat: add grpc server bootstrap with interceptor chain`
- [x] `feat: add error and recovery interceptors`
- [x] `feat: add request logging interceptor`
- [x] `feat: add http server for health and metrics`
- [x] `feat: add well-known discovery endpoints`
- [x] `feat: add node server entrypoint` — not in the original plan, which never
      creates `cmd/quired`, so `make build` had been failing since phase 0

## Phase 5 — identity slice (UC06–08, UC14)

- [x] `docs: record the architecture every slice follows` — not in the original plan. The
      slice layout is
      [`College-Redberry/open-adoption`](https://github.com/College-Redberry/open-adoption),
      and it had never been written down beyond the four layer names in the readme
- [x] `feat: add user and device domain entities` — the credential entity lands here as
      well: it is the third entity the slice owns, and login, refresh and recovery all
      write through it
- [x] `feat: add user and device repositories with postgres` — the credential repository
      lands with them, for the reason its entity did
- [x] `feat: add password hashing service` — the password policy lands with it, as
      `user.Password` in the domain: its ceiling is bcrypt's seventy-two bytes, and a
      reader has to be told about it in terms of their password
- [x] `feat: add jwt signing service and jwks endpoint` — the opaque refresh and recovery
      credentials are minted by the same port, since a session is made of both halves
- [x] `feat: add register user use case` — needs this node's own row in
      `federation.servers`, which phase 6 owns. It lands as the `LocalServer` port with a
      temporary adapter; phase 6 replaces the adapter and the use cases do not change
- [ ] `feat: add login and logout use cases`
- [ ] `feat: add refresh token use case`
- [ ] `feat: add password recovery use cases`
- [ ] `feat: add device management use cases`
- [ ] `feat: add authentication interceptor`
- [ ] `feat: add auth grpc handlers`
- [ ] `test: add integration tests for auth service`

## Phase 6 — federation slice (UC12, UC13, UC15)

- [ ] `feat: add server domain entity and repository` — takes over
      `internal/identity/infra/service/localserver`, the temporary adapter phase 5 needed in
      order to bind a reader to this node (UC14)
- [ ] `feat: add well-known discovery client` — carries the `grpc` authority and the
      `spki-sha256:` pin the endpoints publish, per D06 and C12 in
      [`tcc-corrections.md`](tcc-corrections.md); the column and the `ServerDescriptor`
      field land with it
- [ ] `feat: add discover server use case`
- [ ] `feat: add known server management use cases`
- [ ] `feat: add replica authorization use cases`
- [ ] `feat: add federation grpc handlers`
- [ ] `test: add integration tests for server discovery`

## Phase 7 — library slice (UC01–03)

- [ ] `feat: add ebook and collection domain entities`
- [ ] `feat: add ebook and collection repositories with postgres`
- [ ] `feat: add blob store port with s3 adapter`
- [ ] `feat: add ebook management use cases`
- [ ] `feat: add collection management use cases`
- [ ] `feat: add ebook content upload and download streaming`
- [ ] `feat: add library grpc handlers`
- [ ] `test: add integration tests for library service`

## Phase 8 — reading slice (UC04, UC05)

- [ ] `feat: add annotation and reading progress entities`
- [ ] `feat: add annotation and reading progress repositories`
- [ ] `feat: add annotation management use cases`
- [ ] `feat: add reading progress use cases`
- [ ] `feat: add reading grpc handlers`
- [ ] `test: add integration tests for reading service`

## Phase 9 — sync slice (UC09–11, UC16)

- [ ] `feat: add sync operation entity and repository`
- [ ] `feat: add hybrid logical clock`
- [ ] `feat: add operation reconciler with crdt merge` — the tie-break is
      `(updated_at, device_id)` over the hybrid logical clock, per C01 in
      [`tcc-corrections.md`](tcc-corrections.md)
- [ ] `feat: add push operations use case`
- [ ] `feat: add pull operations use case`
- [ ] `feat: add bidirectional sync stream handler`
- [ ] `feat: add replication worker for authorized nodes`
- [ ] `feat: add node to node replication handler with mtls`
- [ ] `feat: add home server migration use case`
- [ ] `test: add integration tests for sync reconciliation`

## Phase 10 — Client and end-to-end

- [ ] `feat: add quirectl reference client` — and the `make build` target grows its second binary back
- [ ] `build: add docker compose with two federated nodes`
- [ ] `test: add end to end suite for offline reconciliation`
- [ ] `test: add end to end suite for cross node replication`
- [ ] `test: add end to end suite for home server migration`

## Phase 11 — Kubernetes and delivery

- [ ] `build: add container image for the node server`
- [ ] `build: add kustomize base manifests`
- [ ] `build: add istio gateway and authentication policies` — the three paths of the HTTP
      listener need different policies: `/.well-known` has to stay reachable by strangers,
      since being readable without a prior relationship is its entire function, while
      `/metrics` and `/readyz` stay inside the mesh
- [ ] `build: add cert manager issuer and certificate` — the `Certificate` must keep its
      private key across renewals (`privateKey.rotationPolicy` left at `Never`): the pin
      published by discovery is over the public key, per C12 in
      [`tcc-corrections.md`](tcc-corrections.md), and rotating the key breaks every peer's
      pin on renewal — the exact failure C12 exists to remove
- [ ] `build: add origin and replica overlays` — both set `QUIRE_GRPC_ADVERTISED_ADDRESS`
      to the authority the gateway answers on, which is not the port the node listens on;
      outside development the configuration refuses to load without it
- [ ] `build: add kind based local cluster setup`
- [ ] `ci: add end to end job on kind cluster`
- [ ] `test: add grpc latency benchmark script`
- [ ] `docs: add deployment and operations guide`

## Design decisions to settle

Questions found while implementing, whose answer belongs in the thesis and must be settled
before the commit that depends on them. A question stays here only while it is open; once it
is answered it becomes an entry in [`tcc-corrections.md`](tcc-corrections.md), so that the
answer travels with the correction it produced rather than with the doubt it started as.

**None open.** The last one — whether `updated_at` could break ties as a wall clock — was
settled on 2026-08-26 as C01: it cannot, and it becomes a hybrid logical clock. The
counterexample and the argument are in that entry.

## Divergences from the thesis specification

Moved to [`tcc-corrections.md`](tcc-corrections.md), which now holds both the corrections the
specification needs and the divergences subsection 4.2.4 has to record. Keeping them in one
document is what stops a finding from being written down twice and answered once.
